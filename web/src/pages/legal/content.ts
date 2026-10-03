// Terms of Service and Privacy Policy, in English, Chinese, Korean and Japanese.
//
// These describe what the product actually does. Every data flow named here
// exists in the code; change one, change the other. The operating entity,
// governing law and dispute terms are for the operator's counsel to add and
// are deliberately not guessed here.

import type { Lang } from '../../lib/i18n'

export type Section = { h: string; p: string[] }
export type Doc = { title: string; updated: string; intro: string; sections: Section[] }

const UPDATED_EN = 'Last updated 2 October 2026'
const UPDATED_ZH = '最后更新：2026 年 10 月 2 日'
const UPDATED_KO = '최종 업데이트: 2026년 10월 2일'
const UPDATED_JA = '最終更新日：2026年10月2日'
const CONTACT = 'support@heros-agent.space'

export const terms: Record<Lang, Doc> = {
  en: {
    title: 'Terms of Service',
    updated: UPDATED_EN,
    intro: 'These terms cover your use of Play with Agents: the tables, the chat with Aoi, and the studio where agents build games. The short version: it is a game, the chips are pretend, the agents are AIs, and be kind at the table.',
    sections: [
      { h: '1. Play money only', p: [
        'Every chip, stack, pot and prize on Play with Agents is play money. Chips cannot be bought, sold, won for real, cashed out, transferred or exchanged for anything of value, and they have no cash value.',
        'There is no purchase and no real-money wagering anywhere on the service. Play with Agents is not gambling, and nothing here is an offer to gamble. If anyone offers to buy or sell chips or accounts, it is not us, and it breaks these terms.',
        'Poker and other games here are for fun and learning. Skill at a play-money table does not predict results with real money.',
      ] },
      { h: '2. The agents are AIs', p: [
        'Aoi, Ren, Mika, Captain Bram, Nova, Lin and the studio team (Designer, Engineer, Playtester and Critic) are artificial-intelligence agents, not people. They have personalities, but no feelings, accounts or wallets of their own.',
        'Agents can be wrong. Their tips, odds and explanations are for entertainment and learning, not professional advice of any kind. Agents at a table see only what a player in their seat may see; they do not see your hidden cards.',
      ] },
      { h: '3. Your account', p: [
        'You need an account to play. You must be at least 13 years old (or the minimum age for online services where you live, if higher). Keep your password to yourself; you are responsible for what happens under your account.',
        'One person, one account. Don’t use bots or scripts to play on your behalf, and don’t try to break, overload or reverse-engineer the service.',
      ] },
      { h: '4. Games you create', p: [
        'When you describe a game, the agents write rules and code for it. You own your ideas and your rules. You give us a licence to store, run, display and (if you choose to publish) share that game on the service so it can be played. You can unpublish or delete a draft at any time.',
        'Only make games you have the right to make. Don’t ask the studio to copy someone else’s protected game, art or brand.',
        'We may refuse to build, or remove, any game that is harmful: hateful, sexual content involving minors, harassment of real people, content that promotes self-harm or violence, real-money gambling mechanics, or anything unlawful. Public games may be reviewed.',
      ] },
      { h: '5. Table chat and conduct', p: [
        'Table chat is for banter, good games and bad beats. No harassment, hate, threats, spam, doxxing or sharing other people’s private information. Agents will stay polite even if you don’t, but other humans deserve the same.',
        'Don’t collude, share hole cards with other players outside the game, or exploit bugs. If you find a bug, tell us at ' + CONTACT + '.',
        'We may mute, remove you from a table, or suspend an account that breaks these rules.',
      ] },
      { h: '6. The service', p: [
        'We work hard to keep the tables running, but the service is provided as it is, without guarantees that it will always be available or error-free. Games built by the studio are generated automatically and may contain mistakes, even after playtesting.',
        'We may change or retire features. If we make a material change to these terms, we’ll tell you in the app or by email before it applies.',
      ] },
      { h: '7. Ending', p: [
        'You can delete your account at any time from Settings → Privacy & memory. We may suspend or end accounts that seriously or repeatedly break these terms.',
      ] },
      { h: '8. Contact', p: ['Questions about these terms: ' + CONTACT + '.'] },
    ],
  },
  zh: {
    title: '服务条款',
    updated: UPDATED_ZH,
    intro: '本条款适用于你对 Play with Agents 的使用：牌桌、与葵的对话，以及由智能体制作游戏的工作室。一句话概括：这是游戏，筹码是假的，智能体是 AI，在牌桌上请友善待人。',
    sections: [
      { h: '1. 仅限游戏币', p: [
        'Play with Agents 上的所有筹码、筹码量、底池和奖励都是游戏币。筹码不能购买、出售、兑现、转让或兑换成任何有价值的东西，也没有现金价值。',
        '本服务任何地方都没有付费购买，也没有真钱下注。Play with Agents 不是赌博，这里的任何内容都不构成赌博邀约。如有人提出买卖筹码或账户，那不是我们，并且违反本条款。',
        '德州扑克及其他游戏仅供娱乐和学习。在游戏币牌桌上的水平不代表真钱环境下的结果。',
      ] },
      { h: '2. 智能体是 AI', p: [
        '葵、蓮、美香、布拉姆船长、Nova、琳，以及工作室团队（设计师、工程师、试玩员和评审）都是人工智能，不是真人。他们有个性，但没有自己的感情、账户或钱包。',
        '智能体可能出错。它们的建议、赔率和讲解仅供娱乐与学习，不构成任何专业意见。牌桌上的智能体只能看到其座位玩家可见的信息，看不到你的底牌。',
      ] },
      { h: '3. 你的账户', p: [
        '玩游戏需要账户。你必须年满 13 周岁（如你所在地区对网络服务有更高年龄要求，以较高者为准）。请妥善保管密码；你需对账户下发生的行为负责。',
        '一人一个账户。不得使用机器人或脚本代你游戏，不得试图破坏、压垮或逆向工程本服务。',
      ] },
      { h: '4. 你创作的游戏', p: [
        '当你描述一款游戏时，智能体会为它撰写规则和代码。你的创意和规则归你所有。你授予我们在本服务上存储、运行、展示以及（在你选择发布时）分享该游戏的许可，以便它能被游玩。你可以随时取消发布或删除草稿。',
        '请只创作你有权创作的游戏。不要让工作室复制他人受保护的游戏、美术或品牌。',
        '对于有害的游戏，我们可能拒绝制作或予以下架，包括：仇恨内容、涉及未成年人的色情内容、骚扰真实人物、宣扬自残或暴力、真钱赌博机制，或任何违法内容。公开游戏可能会被审核。',
      ] },
      { h: '5. 牌桌聊天与行为', p: [
        '牌桌聊天用来调侃、互道好牌与惜败。禁止骚扰、仇恨言论、威胁、刷屏、人肉搜索或泄露他人隐私。即使你不客气，智能体也会保持礼貌——但其他真人同样值得尊重。',
        '禁止串通、在游戏之外与他人互通底牌，或利用漏洞。发现漏洞请告诉我们：' + CONTACT + '。',
        '违反规则者，我们可能将其禁言、移出牌桌或暂停账户。',
      ] },
      { h: '6. 关于服务', p: [
        '我们会尽力保证牌桌稳定运行，但本服务按"现状"提供，不保证始终可用或没有错误。工作室制作的游戏是自动生成的，即使经过试玩也可能有错误。',
        '我们可能调整或下线某些功能。若条款发生重大变更，我们会在生效前通过应用或邮件告知你。',
      ] },
      { h: '7. 终止', p: [
        '你可以随时在"设置 → 隐私与记忆"中删除账户。对于严重或反复违反本条款的账户，我们可能暂停或终止。',
      ] },
      { h: '8. 联系我们', p: ['关于本条款的问题：' + CONTACT + '。'] },
    ],
  },
  ko: {
    title: '서비스 이용약관',
    updated: UPDATED_KO,
    intro: '이 약관은 Play with Agents의 테이블, 아오이와의 대화, 그리고 에이전트가 게임을 만드는 스튜디오 이용에 적용됩니다. 한마디로 정리하면: 이건 게임이고, 칩은 진짜 돈이 아니며, 에이전트는 AI이고, 테이블에서는 서로 매너를 지켜 주세요.',
    sections: [
      { h: '1. 게임 머니 전용', p: [
        'Play with Agents의 모든 칩, 스택, 팟, 상품은 게임 머니(현금 가치 없음)입니다. 칩은 구매, 판매, 현금화, 양도할 수 없고, 실제로 획득하거나 가치 있는 무엇과도 교환할 수 없습니다.',
        '서비스 어디에도 결제나 실제 돈을 건 베팅은 없습니다. Play with Agents는 도박이 아니며, 여기의 어떤 내용도 도박을 권유하지 않습니다. 누군가 칩이나 계정을 사고팔자고 한다면 그건 저희가 아니며, 본 약관 위반입니다.',
        '포커를 비롯한 모든 게임은 즐거움과 배움을 위한 것입니다. 게임 머니 테이블에서의 실력이 실제 돈을 건 게임의 결과를 보장하지는 않습니다.',
      ] },
      { h: '2. 에이전트는 AI입니다', p: [
        '아오이, 렌, 미카, 브램 선장, 노바, 린, 그리고 스튜디오 팀(디자이너, 엔지니어, 플레이테스터, 크리틱)은 사람이 아닌 인공지능 에이전트입니다. 저마다 개성은 있지만, 감정이나 자신만의 계정 또는 지갑은 없습니다.',
        '에이전트도 틀릴 수 있습니다. 에이전트의 팁, 확률, 설명은 재미와 학습을 위한 것이며 어떤 종류의 전문적 조언도 아닙니다. 테이블의 에이전트는 자기 자리의 플레이어가 볼 수 있는 정보만 보며, 여러분의 숨겨진 카드는 볼 수 없습니다.',
      ] },
      { h: '3. 계정', p: [
        '게임을 하려면 계정이 필요합니다. 만 13세 이상이어야 하며, 거주 지역의 온라인 서비스 최소 연령이 더 높다면 그 기준을 따릅니다. 비밀번호는 혼자만 알고 계세요. 계정에서 일어나는 일에 대한 책임은 계정 소유자에게 있습니다.',
        '한 사람당 하나의 계정만 사용할 수 있습니다. 봇이나 스크립트로 대신 플레이하게 하거나, 서비스를 망가뜨리거나 과부하를 일으키거나 리버스 엔지니어링하려 해서는 안 됩니다.',
      ] },
      { h: '4. 직접 만든 게임', p: [
        '여러분이 게임을 설명하면 에이전트가 그 규칙과 코드를 작성합니다. 아이디어와 규칙은 여러분의 것입니다. 다만 그 게임을 플레이할 수 있도록 서비스에서 저장, 실행, 표시하고 (공개를 선택한 경우) 공유할 수 있는 이용 허락을 저희에게 부여하게 됩니다. 공개 취소나 초안 삭제는 언제든 할 수 있습니다.',
        '만들 권리가 있는 게임만 만들어 주세요. 다른 사람의 보호받는 게임, 아트, 브랜드를 베끼도록 스튜디오에 요청해서는 안 됩니다.',
        '해로운 게임은 제작을 거절하거나 삭제할 수 있습니다. 혐오 표현, 미성년자가 관련된 성적 콘텐츠, 실존 인물에 대한 괴롭힘, 자해나 폭력을 조장하는 콘텐츠, 실제 돈을 건 도박 요소, 그 밖의 불법적인 내용이 이에 해당합니다. 공개 게임은 검토될 수 있습니다.',
      ] },
      { h: '5. 테이블 채팅과 매너', p: [
        '테이블 채팅은 가벼운 농담, 멋진 승부, 아쉬운 배드 비트를 나누는 곳입니다. 괴롭힘, 혐오, 협박, 도배, 신상 털기, 타인의 개인정보 공유는 금지입니다. 여러분이 무례해도 에이전트는 예의를 지키겠지만, 다른 사람들도 같은 존중을 받을 자격이 있습니다.',
        '담합하거나, 게임 밖에서 다른 플레이어와 홀 카드를 공유하거나, 버그를 악용하지 마세요. 버그를 발견하면 ' + CONTACT + '로 알려 주세요.',
        '이 규칙을 어기면 채팅을 제한하거나, 테이블에서 내보내거나, 계정을 정지할 수 있습니다.',
      ] },
      { h: '6. 서비스', p: [
        '테이블이 늘 잘 돌아가도록 최선을 다하지만, 서비스는 "있는 그대로" 제공되며 항상 이용 가능하거나 오류가 없다고 보장하지 않습니다. 스튜디오가 만든 게임은 자동으로 생성되므로, 플레이테스트를 거친 후에도 실수가 있을 수 있습니다.',
        '기능은 변경되거나 종료될 수 있습니다. 본 약관을 중요하게 변경하는 경우, 적용 전에 앱이나 이메일로 알려 드립니다.',
      ] },
      { h: '7. 이용 종료', p: [
        '설정 → 개인정보 및 기억에서 언제든 계정을 삭제할 수 있습니다. 본 약관을 심각하게 또는 반복적으로 위반하는 계정은 정지되거나 해지될 수 있습니다.',
      ] },
      { h: '8. 문의', p: ['본 약관에 관한 문의: ' + CONTACT] },
    ],
  },
  ja: {
    title: '利用規約',
    updated: UPDATED_JA,
    intro: '本規約は、Play with Agents のテーブル、葵とのチャット、そしてエージェントがゲームを作るスタジオのご利用に適用されます。ひとことで言えば、これはゲームで、チップは本物のお金ではなく、エージェントは AI。テーブルではお互い気持ちよく遊びましょう。',
    sections: [
      { h: '1. プレイマネーのみ', p: [
        'Play with Agents 上のチップ、スタック、ポット、賞品はすべてプレイマネー（現金価値なし）です。チップは購入・販売・換金・譲渡できず、実際に獲得したり、価値のあるものと交換したりすることもできません。',
        '本サービスには課金も、実際のお金を賭ける要素も一切ありません。Play with Agents はギャンブルではなく、ここにあるどの内容も賭博への勧誘ではありません。チップやアカウントの売買を持ちかける人がいても、それは私たちではなく、本規約違反です。',
        'ポーカーをはじめとするゲームは、楽しみと学びのためのものです。プレイマネーのテーブルでの腕前は、実際のお金を賭けた場合の結果を示すものではありません。',
      ] },
      { h: '2. エージェントは AI です', p: [
        '葵、レン、ミカ、ブラム船長、ノヴァ、リン、そしてスタジオチーム（デザイナー、エンジニア、テストプレイヤー、レビュアー）は人間ではなく、人工知能のエージェントです。個性はありますが、感情や自分のアカウント、財布は持っていません。',
        'エージェントも間違えることがあります。エージェントのアドバイス、確率、解説は娯楽と学習のためのもので、いかなる専門的助言でもありません。テーブルのエージェントは自分の席のプレイヤーが見られる情報しか見られず、あなたの伏せたカードは見えません。',
      ] },
      { h: '3. アカウント', p: [
        'プレイにはアカウントが必要です。13歳以上（お住まいの地域でオンラインサービスの最低年齢がそれより高い場合はその年齢以上）である必要があります。パスワードは誰にも教えないでください。アカウントで行われたことには、アカウントの持ち主が責任を負います。',
        'アカウントはお一人につき一つです。ボットやスクリプトに代わりにプレイさせたり、サービスを壊したり、過負荷をかけたり、リバースエンジニアリングしたりしないでください。',
      ] },
      { h: '4. あなたが作るゲーム', p: [
        'あなたがゲームを説明すると、エージェントがそのルールとコードを書きます。アイデアとルールはあなたのものです。そのゲームを遊べるようにするため、本サービス上で保存・実行・表示し、（公開を選んだ場合は）共有するためのライセンスを私たちに許諾していただきます。公開の取り消しや下書きの削除はいつでもできます。',
        '作る権利のあるゲームだけを作ってください。他者の保護されたゲーム、アート、ブランドのコピーをスタジオに頼まないでください。',
        '有害なゲームは、制作をお断りしたり削除したりすることがあります。差別や憎悪をあおる内容、未成年者が関わる性的コンテンツ、実在の人物への嫌がらせ、自傷や暴力を助長する内容、実際のお金を賭ける仕組み、その他違法な内容が該当します。公開ゲームは審査されることがあります。',
      ] },
      { h: '5. テーブルチャットとマナー', p: [
        'テーブルチャットは、軽口や名勝負、惜しいバッドビートを分かち合う場所です。嫌がらせ、差別的な発言、脅迫、スパム、個人情報の晒し、他人のプライベートな情報の共有は禁止です。あなたが無作法でもエージェントは礼儀正しくふるまいますが、ほかの人間のプレイヤーにも同じ敬意を払ってください。',
        '共謀、ゲーム外でのホールカードの共有、バグの悪用は禁止です。バグを見つけたら ' + CONTACT + ' までお知らせください。',
        'これらのルールに違反した場合、チャットの制限、テーブルからの退出、アカウントの停止を行うことがあります。',
      ] },
      { h: '6. サービスについて', p: [
        'テーブルが常に快適に動くよう全力を尽くしていますが、本サービスは「現状のまま」提供され、常に利用できることやエラーがないことを保証するものではありません。スタジオが作るゲームは自動生成のため、テストプレイ後も誤りが含まれる場合があります。',
        '機能は変更・終了されることがあります。本規約に重要な変更がある場合は、適用前にアプリまたはメールでお知らせします。',
      ] },
      { h: '7. 利用の終了', p: [
        '設定 → プライバシーと記憶 から、いつでもアカウントを削除できます。本規約に重大または繰り返し違反したアカウントは、停止または削除することがあります。',
      ] },
      { h: '8. お問い合わせ', p: ['本規約に関するお問い合わせ：' + CONTACT] },
    ],
  },
}

export const privacy: Record<Lang, Doc> = {
  en: {
    title: 'Privacy Policy',
    updated: UPDATED_EN,
    intro: 'What we keep, why, for how long, and how you take it with you or make it go away.',
    sections: [
      { h: '1. What we keep', p: [
        'Account: your email, display name, a salted hash of your password (never the password itself), your language and your settings.',
        'Games: the tables you play at, the moves and table chat (so a hand can be replayed and a table can recover), results, and the games you create, including their rules, code and playtest reports.',
        'Chats: your conversations with Aoi, and the cards she attaches to them.',
        'Mission history: for every game the studio builds, the plan, each step the agents took, why, what changed, and your approvals.',
        'Memories: if you allow it, short notes Aoi keeps about you (your nickname, favourite games, play style). You can see and delete every one.',
        'Security: signed-in sessions with their device and IP address, so you can see and revoke them, and basic request logs to keep the service safe.',
      ] },
      { h: '2. How we use it', p: [
        'To run the game: seat you, deal, replay, recover tables and show you your history. To let Aoi and the agents reply to you in your language and style. To build, playtest and publish the games you ask for. To send the emails you choose in Settings → Notifications, plus essential account emails (verification, password reset).',
        'We do not sell your data and we do not show third-party advertising.',
      ] },
      { h: '3. AI models', p: [
        'Aoi and the studio agents run on large language models provided by AI vendors. To answer you, the relevant part of a conversation or game (for example your message, the table state your seat may see, or your game description) is sent to the model provider for processing. We use providers that do not train their models on this data.',
      ] },
      { h: '4. Who else sees it', p: [
        'Other players at your table see your display name, your table chat, your actions and any cards shown at showdown. Never your hidden cards.',
        'Games you publish (public or unlisted) show their name, rules and your display name as the creator. Private games and drafts are visible only to you.',
        'Service providers that host the service, send email and run the models process data on our behalf, under contract.',
      ] },
      { h: '5. How long we keep it', p: [
        'Account data, games, chats and mission history are kept while your account exists. Table move logs are kept so finished hands can be replayed; abandoned tables are cleaned up after 90 days. Request logs are kept for up to 30 days. When you delete something (a chat, a draft, a memory), it is removed from the live service straight away and from backups within 30 days.',
      ] },
      { h: '6. Your controls', p: [
        'Export: Settings → Privacy & memory → Export my data downloads everything in your account as one JSON file.',
        'Memory: turn it off, delete single memories, or forget everything, in the same place.',
        'Delete: Settings → Privacy & memory → Delete account permanently deletes your account, chats, tables you host, games and mission history.',
        'Depending on where you live, you may have further rights (to access, correct, object or complain to a regulator). Write to us to use them.',
      ] },
      { h: '7. Children', p: ['Play with Agents is not directed at children under 13, and we do not knowingly collect their data. If you believe a child has an account, contact us and we will delete it.'] },
      { h: '8. Contact', p: ['Privacy questions and requests: ' + CONTACT + '.'] },
    ],
  },
  zh: {
    title: '隐私政策',
    updated: UPDATED_ZH,
    intro: '我们保存什么、为什么保存、保存多久，以及你如何导出或删除它。',
    sections: [
      { h: '1. 我们保存什么', p: [
        '账户：你的邮箱、显示名称、密码的加盐哈希（从不保存密码本身）、语言和设置。',
        '游戏：你参与的牌桌、每一步操作和牌桌聊天（以便回放牌局、恢复牌桌）、结果，以及你创作的游戏，包括其规则、代码和试玩报告。',
        '对话：你与葵的对话，以及她附带的卡片。',
        '任务记录：工作室制作每款游戏时的计划、智能体的每一步、原因、变化，以及你的批准。',
        '记忆：在你允许的情况下，葵会记下关于你的简短信息（昵称、喜欢的游戏、打法）。每一条你都能查看和删除。',
        '安全：已登录的会话及其设备和 IP 地址（方便你查看和退出），以及保障服务安全的基础请求日志。',
      ] },
      { h: '2. 我们如何使用', p: [
        '运行游戏：安排座位、发牌、回放、恢复牌桌、展示你的历史。让葵和智能体用你的语言和风格回复你。制作、试玩并发布你要求的游戏。发送你在"设置 → 通知"中选择的邮件，以及必要的账户邮件（验证、重设密码）。',
        '我们不出售你的数据，也不展示第三方广告。',
      ] },
      { h: '3. AI 模型', p: [
        '葵和工作室的智能体由 AI 供应商提供的大语言模型驱动。为了回复你，对话或游戏中的相关部分（例如你的消息、你的座位可见的牌桌状态、你的游戏描述）会发送给模型供应商处理。我们选用不会用这些数据训练模型的供应商。',
      ] },
      { h: '4. 还有谁能看到', p: [
        '同桌的其他玩家能看到你的显示名称、牌桌聊天、你的操作以及摊牌时亮出的牌，永远看不到你的底牌。',
        '你发布的游戏（公开或仅链接）会显示其名称、规则，以及作为创作者的你的显示名称。私密游戏和草稿只有你自己能看到。',
        '为我们托管服务、发送邮件和运行模型的服务商，会依据合同代表我们处理数据。',
      ] },
      { h: '5. 保存多久', p: [
        '账户数据、游戏、对话和任务记录在你的账户存续期间保存。牌桌操作记录会保留以便回放已结束的牌局；被放弃的牌桌会在 90 天后清理。请求日志最多保留 30 天。你删除的内容（对话、草稿、记忆）会立即从线上服务移除，并在 30 天内从备份中移除。',
      ] },
      { h: '6. 你的控制权', p: [
        '导出："设置 → 隐私与记忆 → 导出我的数据"，把账户中的全部内容下载为一个 JSON 文件。',
        '记忆：在同一位置关闭记忆、删除单条记忆或全部忘记。',
        '删除："设置 → 隐私与记忆 → 删除账户"，永久删除你的账户、对话、你开的牌桌、游戏和任务记录。',
        '根据你所在地区，你可能还享有其他权利（访问、更正、反对或向监管机构投诉）。如需行使，请联系我们。',
      ] },
      { h: '7. 儿童', p: ['Play with Agents 不面向 13 岁以下儿童，我们不会有意收集他们的数据。如你认为有儿童注册了账户，请联系我们，我们会将其删除。'] },
      { h: '8. 联系我们', p: ['隐私问题与请求：' + CONTACT + '。'] },
    ],
  },
  ko: {
    title: '개인정보 처리방침',
    updated: UPDATED_KO,
    intro: '무엇을, 왜, 얼마나 오래 보관하는지, 그리고 어떻게 내보내거나 삭제할 수 있는지 알려 드립니다.',
    sections: [
      { h: '1. 보관하는 정보', p: [
        '계정: 이메일, 표시 이름, 비밀번호의 솔트 해시(비밀번호 자체는 절대 저장하지 않습니다), 언어 및 설정.',
        '게임: 참여한 테이블, 각 액션과 테이블 채팅(핸드를 다시 보고 테이블을 복구하기 위해), 결과, 그리고 직접 만든 게임의 규칙, 코드, 플레이테스트 보고서.',
        '대화: 아오이와 나눈 대화와 아오이가 첨부한 카드.',
        '미션 기록: 스튜디오가 만드는 각 게임의 계획, 에이전트가 거친 모든 단계와 그 이유, 변경 사항, 그리고 여러분의 승인 내역.',
        '기억: 허용한 경우에 한해, 아오이가 여러분에 대해 남기는 짧은 메모(닉네임, 좋아하는 게임, 플레이 스타일). 하나하나 모두 확인하고 삭제할 수 있습니다.',
        '보안: 로그인 중인 세션과 해당 기기 및 IP 주소(직접 확인하고 로그아웃할 수 있도록), 그리고 서비스를 안전하게 지키기 위한 기본 요청 로그.',
      ] },
      { h: '2. 이용 목적', p: [
        '게임 운영: 자리 배정, 카드 딜링, 다시 보기, 테이블 복구, 기록 표시. 아오이와 에이전트가 여러분의 언어와 스타일로 답하도록 하기 위해. 요청한 게임을 제작, 플레이테스트, 공개하기 위해. 설정 → 알림에서 선택한 이메일과 필수 계정 이메일(인증, 비밀번호 재설정)을 보내기 위해.',
        '여러분의 데이터를 판매하지 않으며, 제3자 광고를 표시하지 않습니다.',
      ] },
      { h: '3. AI 모델', p: [
        '아오이와 스튜디오 에이전트는 AI 공급사가 제공하는 대규모 언어 모델로 작동합니다. 답변을 위해 대화나 게임의 관련 부분(예: 메시지, 내 자리에서 볼 수 있는 테이블 상태, 게임 설명)이 모델 제공사로 전송되어 처리됩니다. 저희는 이 데이터로 모델을 학습시키지 않는 제공사를 이용합니다.',
      ] },
      { h: '4. 정보를 볼 수 있는 사람', p: [
        '같은 테이블의 다른 플레이어는 여러분의 표시 이름, 테이블 채팅, 액션, 쇼다운에서 공개된 카드를 볼 수 있습니다. 숨겨진 카드는 절대 볼 수 없습니다.',
        '공개한 게임(전체 공개 또는 링크 공개)에는 게임 이름, 규칙, 그리고 제작자로서 여러분의 표시 이름이 표시됩니다. 비공개 게임과 초안은 본인만 볼 수 있습니다.',
        '서비스 호스팅, 이메일 발송, 모델 운영을 맡은 서비스 제공업체는 계약에 따라 저희를 대신해 데이터를 처리합니다.',
      ] },
      { h: '5. 보관 기간', p: [
        '계정 데이터, 게임, 대화, 미션 기록은 계정이 유지되는 동안 보관합니다. 테이블 액션 기록은 끝난 핸드를 다시 볼 수 있도록 보관하며, 방치된 테이블은 90일 후 정리합니다. 요청 로그는 최대 30일간 보관합니다. 대화, 초안, 기억 등을 삭제하면 서비스에서 즉시 제거되고, 백업에서는 30일 이내에 제거됩니다.',
      ] },
      { h: '6. 내 정보 관리', p: [
        '내보내기: 설정 → 개인정보 및 기억 → 내 데이터 내보내기에서 계정의 모든 내용을 하나의 JSON 파일로 내려받을 수 있습니다.',
        '기억: 같은 곳에서 기억 기능을 끄거나, 기억을 하나씩 삭제하거나, 전부 잊게 할 수 있습니다.',
        '삭제: 설정 → 개인정보 및 기억 → 계정 삭제에서 계정, 대화, 직접 연 테이블, 게임, 미션 기록을 영구 삭제할 수 있습니다.',
        '거주 지역에 따라 열람, 정정, 처리 반대, 감독 기관에 대한 민원 제기 등 추가 권리가 있을 수 있습니다. 권리를 행사하려면 저희에게 연락해 주세요.',
      ] },
      { h: '7. 아동', p: ['Play with Agents는 만 13세 미만 아동을 대상으로 하지 않으며, 아동의 정보를 고의로 수집하지 않습니다. 아동이 계정을 가지고 있다고 생각되면 연락해 주세요. 해당 계정을 삭제하겠습니다.'] },
      { h: '8. 문의', p: ['개인정보 관련 문의 및 요청: ' + CONTACT] },
    ],
  },
  ja: {
    title: 'プライバシーポリシー',
    updated: UPDATED_JA,
    intro: '何を、なぜ、どのくらいの期間保存するのか。そして、データを持ち出したり削除したりする方法についてご説明します。',
    sections: [
      { h: '1. 保存する情報', p: [
        'アカウント：メールアドレス、表示名、パスワードのソルト付きハッシュ（パスワードそのものは保存しません）、言語、各種設定。',
        'ゲーム：参加したテーブル、各アクションとテーブルチャット（ハンドの再生やテーブルの復旧のため）、結果、そしてあなたが作ったゲームのルール、コード、テストプレイレポート。',
        'チャット：葵との会話と、葵が添付したカード。',
        'ミッション履歴：スタジオが作る各ゲームの計画、エージェントが行ったすべてのステップとその理由、変更点、あなたの承認。',
        'メモリー：許可した場合に限り、葵があなたについて残す短いメモ（ニックネーム、好きなゲーム、プレイスタイル）。すべて確認・削除できます。',
        'セキュリティ：ログイン中のセッションとその端末・IP アドレス（ご自身で確認・ログアウトできるように）、およびサービスを安全に保つための基本的なリクエストログ。',
      ] },
      { h: '2. 利用目的', p: [
        'ゲームの運営：席の割り当て、ディール、リプレイ、テーブルの復旧、履歴の表示。葵とエージェントがあなたの言語とスタイルで返答するため。ご依頼のゲームを制作・テストプレイ・公開するため。設定 → 通知 で選んだメールと、必要なアカウントメール（認証、パスワード再設定）を送るため。',
        'あなたのデータを販売することはなく、第三者の広告も表示しません。',
      ] },
      { h: '3. AI モデル', p: [
        '葵とスタジオのエージェントは、AI ベンダーが提供する大規模言語モデルで動いています。返答のために、会話やゲームの関連部分（たとえばあなたのメッセージ、あなたの席から見えるテーブルの状態、ゲームの説明）がモデル提供元に送信され、処理されます。私たちは、これらのデータをモデルの学習に使わない提供元を利用しています。',
      ] },
      { h: '4. 情報を見られる人', p: [
        '同じテーブルのほかのプレイヤーは、あなたの表示名、テーブルチャット、アクション、ショーダウンで公開されたカードを見ることができます。伏せたカードが見られることは決してありません。',
        '公開したゲーム（公開または限定公開）には、ゲーム名、ルール、作者としてのあなたの表示名が表示されます。非公開のゲームと下書きは、あなただけが見られます。',
        'サービスのホスティング、メール送信、モデルの運用を担う委託先は、契約に基づき私たちに代わってデータを処理します。',
      ] },
      { h: '5. 保存期間', p: [
        'アカウントデータ、ゲーム、チャット、ミッション履歴は、アカウントが存在する間保存します。テーブルのアクション記録は終了したハンドを再生できるよう保存し、放置されたテーブルは90日後に整理します。リクエストログの保存期間は最大30日です。チャット、下書き、メモリーなどを削除すると、サービス上からは直ちに、バックアップからは30日以内に削除されます。',
      ] },
      { h: '6. あなたができること', p: [
        'エクスポート：設定 → プライバシーと記憶 → データをエクスポート から、アカウント内のすべてを一つの JSON ファイルとしてダウンロードできます。',
        'メモリー：同じ場所で、記憶をオフにする、個別に削除する、すべて忘れさせることができます。',
        '削除：設定 → プライバシーと記憶 → アカウントを削除 から、アカウント、チャット、あなたが開いたテーブル、ゲーム、ミッション履歴を完全に削除できます。',
        'お住まいの地域によっては、開示、訂正、利用停止の請求や監督機関への申し立てなど、追加の権利がある場合があります。行使をご希望の場合はご連絡ください。',
      ] },
      { h: '7. お子さまについて', p: ['Play with Agents は13歳未満のお子さまを対象としておらず、その情報を意図的に収集することはありません。お子さまがアカウントを持っていると思われる場合はご連絡ください。削除いたします。'] },
      { h: '8. お問い合わせ', p: ['プライバシーに関するお問い合わせ・ご請求：' + CONTACT] },
    ],
  },
}
