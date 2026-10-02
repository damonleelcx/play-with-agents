#!/usr/bin/env bash
# Deploy ACT to the k3s node over SSM. Every step is idempotent: running this
# twice lands the same state as running it once.
#
#   INSTANCE=i-… deploy/deploy.sh 373468206837.dkr.ecr.us-east-1.amazonaws.com/act@sha256:…
#
# Prerequisites (one-time, see deploy/README.md):
#   - Secrets Manager `act/prod` with ACT_DATABASE_URL and ACT_LLM_API_KEY
#   - node role inline policy ActSecretsRead on secret:act/*
#   - Route53 A record act.heros-agent.space → the node's public IP
set -euo pipefail
IMAGE="${1:?usage: deploy.sh <registry/act@sha256:...> [--dry-run]}"
DRY="${2:-}"
[[ "$IMAGE" == *@sha256:* ]] || { echo "pin the image by digest (…@sha256:…), not a tag" >&2; exit 2; }
HERE="$(cd "$(dirname "$0")" && pwd)"
export INSTANCE="${INSTANCE:?set INSTANCE}"

say() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }

# ── 1. Render manifests with the pinned image ─────────────────────────────
say "rendering manifests"
OUT=$(mktemp)
trap 'rm -f "$OUT"' EXIT
for f in "$HERE"/k8s/*.yaml; do
  # A separator between files, never a plain cat: concatenation silently
  # merges the last document of one file into the first of the next.
  printf -- '---\n'
  sed "s|ACT_IMAGE|${IMAGE}|g" "$f"
  printf '\n'
done > "$OUT"
grep -q "ACT_IMAGE" "$OUT" && { echo "unsubstituted ACT_IMAGE" >&2; exit 1; }
SUM=$(sha256sum "$OUT" | cut -d' ' -f1)
PAYLOAD=$(gzip -9 < "$OUT" | base64 | tr -d '\n')
echo "    $(grep -c '^---' "$OUT") documents, sha256 ${SUM:0:16}…"

APPLY="k3s kubectl apply -f /opt/act/act.yaml"
[ "$DRY" = "--dry-run" ] && APPLY="k3s kubectl apply --dry-run=server -f /opt/act/act.yaml"

# ── 2. On the node ────────────────────────────────────────────────────────
say "bootstrapping database, admitting ACT to shared services, applying"
"$HERE/ssm.sh" "$(cat <<REMOTE
set -euo pipefail
K="k3s kubectl"
mkdir -p /opt/act
echo '${PAYLOAD}' | base64 -d | gunzip > /opt/act/act.yaml
got=\$(sha256sum /opt/act/act.yaml | cut -d' ' -f1)
[ "\$got" = "${SUM}" ] || { echo "checksum mismatch — manifest truncated in transit" >&2; exit 1; }
echo "checksum verified on node"

DRY="${DRY}"
if [ "\$DRY" = "--dry-run" ]; then
  echo "DRY RUN: would bootstrap the act database, admit act in heros/postgres + heros/mail policies, add act to the backup job"
  aws secretsmanager describe-secret --region us-east-1 --secret-id act/prod --query Name --output text
  aws secretsmanager get-secret-value --region us-east-1 --secret-id act/prod --query Name --output text
else
# (a) Database. The DSN is read HERE with the node's own role, so the
# password never travels in this SSM payload (which AWS retains).
DSN=\$(aws secretsmanager get-secret-value --region us-east-1 --secret-id act/prod --query SecretString --output text \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["ACT_DATABASE_URL"])')
USR=\$(printf '%s' "\$DSN" | sed -E 's|^postgres://([^:]+):.*|\1|')
PW=\$(printf '%s' "\$DSN" | sed -E 's|^postgres://[^:]+:([^@]+)@.*|\1|')
DB=\$(printf '%s' "\$DSN" | sed -E 's|^.*/([^/?]+)(\?.*)?$|\1|')
[ -n "\$USR" ] && [ -n "\$PW" ] && [ -n "\$DB" ] || { echo "could not parse the DSN" >&2; exit 1; }
\$K -n heros exec -i postgres-0 -- env PGPW="\$PW" PGUSR="\$USR" PGDB="\$DB" psql -U heros -d postgres -v ON_ERROR_STOP=1 -q <<'SQL'
\set pw \`echo "\$PGPW"\`
\set usr \`echo "\$PGUSR"\`
\set db \`echo "\$PGDB"\`
SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'usr', :'pw') WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'usr')\gexec
SELECT format('ALTER ROLE %I WITH LOGIN PASSWORD %L', :'usr', :'pw')\gexec
SELECT format('CREATE DATABASE %I OWNER %I', :'db', :'usr') WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = :'db')\gexec
-- CONNECT is granted to PUBLIC by default: revoke it from PUBLIC (revoking
-- from the role alone does nothing) and hand it back to the owner.
SELECT format('REVOKE CONNECT ON DATABASE %I FROM PUBLIC', :'db')\gexec
SELECT format('GRANT CONNECT ON DATABASE %I TO %I', :'db', :'usr')\gexec
SQL
echo "database \$DB ready for role \$USR"

# (b) Shared services admit ACT pods by namespace AND label — additive
# patches, skipped when the rule is already present.
PEER='{"namespaceSelector":{"matchLabels":{"kubernetes.io/metadata.name":"act"}},"podSelector":{"matchLabels":{"app.kubernetes.io/part-of":"act"}}}'
if \$K -n heros get networkpolicy postgres -o json | grep -q '"kubernetes.io/metadata.name":"act"'; then
  echo "postgres policy already admits act"
else
  \$K -n heros patch networkpolicy postgres --type=json -p "[{\"op\":\"add\",\"path\":\"/spec/ingress/0/from/-\",\"value\":\$PEER}]"
fi
if \$K -n heros get networkpolicy mail -o json | grep -q '"kubernetes.io/metadata.name":"act"'; then
  echo "mail policy already admits act"
else
  \$K -n heros patch networkpolicy mail --type=json -p "[{\"op\":\"add\",\"path\":\"/spec/ingress/-\",\"value\":{\"from\":[\$PEER],\"ports\":[{\"port\":587,\"protocol\":\"TCP\"}]}}]"
fi

# (c) Nightly backup covers only the databases it names.
CUR=\$(\$K -n heros get cronjob postgres-backup -o jsonpath='{.spec.jobTemplate.spec.template.spec.containers[0].env[?(@.name=="BACKUP_DATABASES")].value}')
case " \$CUR " in
  *" act "*) echo "backup already includes act" ;;
  *) \$K -n heros set env cronjob/postgres-backup BACKUP_DATABASES="\$CUR act" && echo "backup now covers: \$CUR act" ;;
esac
fi

# (d) Apply, then wait for both tiers.
echo "--- diff"
\$K diff -f /opt/act/act.yaml || true
echo "--- apply"
${APPLY}
if [ "${DRY}" != "--dry-run" ]; then
  \$K -n act rollout status deploy/act-web --timeout=240s
  \$K -n act rollout status deploy/act-worker --timeout=240s
  \$K -n act get pods,svc,ingress,certificate,externalsecret
fi
REMOTE
)"
say "done"
