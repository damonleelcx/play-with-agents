import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'

export type Lang = 'en' | 'zh'

// Every user-facing string lives here, in both languages. Keys are grouped by
// surface; a missing key renders the key itself so it is obvious in review.
const en = {
  brand: { name: 'ACT', expand: 'Advocacy · Care · Trust', agent: 'Vera' },
  nav: { vera: 'Meet Vera', what: 'What she does', how: 'How it works', faq: 'Questions', signin: 'Sign in', start: 'Get started', app: 'Open app' },
  hero: {
    left: 'COUNSEL', right1: 'AND', right2: 'CARE',
    tagline: 'One companion for the courtroom and the clinic.',
    sub: 'Vera prepares your case and looks after your health — day and night — with licensed attorneys and physicians signing off on everything that matters.',
    meetTitle: 'Meet Vera', meetSub: 'See who will be looking after you.',
    focusTitle: 'Focus on what matters today',
    focusSub: 'Tell her what happened. She organises the facts, finds the rules that protect you, and keeps every date.',
    caption: 'A companion who keeps working while you rest — in English and 中文',
    scenes: ['Legal', 'Health', 'Both'],
    cards: [
      {
        rows: [['Security deposit', 'High', 'In progress', '3d'], ['Demand letter', 'Yours', 'To approve', '1m']],
        panel: 'This week', panelSub: 'What Vera is working on',
        items: [['Mon · 09:00', 'Hearing kit', 'Ready for attorney', 'o'], ['Thu · 14:00', 'Response deadline', 'Reminder set', 'y']],
      },
      {
        rows: [['Blood pressure', 'Weekly', 'On track', '3w'], ['Lab results', 'Doctor', 'Reviewed', '2d']],
        panel: 'Your care', panelSub: 'Check-ins and reviews',
        items: [['Tue · 08:30', 'BP check-in', 'Week 3 of 4', 'o'], ['Fri · 10:00', 'Medication review', 'With physician', 'y']],
      },
      {
        rows: [['Car accident claim', 'High', 'Records', '5d'], ['Insurance appeal', 'Due', 'Oct 14', '3w']],
        panel: 'Case & care', panelSub: 'One file for both sides',
        items: [['Wed · 11:00', 'Medical summary', 'For your claim', 'o'], ['Oct 14', 'Appeal deadline', 'Packet ready', 'y']],
      },
    ],
  },
  meet: {
    eyebrow: 'Meet Vera · 维拉',
    title: 'Her name means truth.',
    body: 'Vera is an AI companion made to sit beside you through two of life’s hardest rooms. She listens first, explains things in plain words, and never pretends to be something she isn’t. She is not a lawyer or a doctor — she works with ours.',
    quote: '“Tell me what happened, in your own words. We’ll take it one step at a time.”',
    values: [
      ['Truth before comfort', 'She tells you what’s likely — not just what’s easy to hear.'],
      ['Your safety first', 'If something sounds urgent, she says so before anything else.'],
      ['Shows her work', 'Every letter, every source, every step. You can always see why.'],
      ['Knows her lane', 'Anything that needs a licence goes to a licensed person.'],
      ['Never forgets a date', 'Deadlines, hearings, check-ins — she keeps them and reminds you.'],
    ],
    moods: ['When she’s listening', 'When she’s thinking', 'When it’s good news'],
    chips: ['Speaks English & 中文', 'Awake 24/7', 'Remembers only what you allow'],
  },
  what: {
    eyebrow: 'What she does for you',
    title: 'Help for the moments that can’t wait.',
    tabs: ['Legal troubles', 'Your health', 'When it’s both'],
    items: [
      [
        ['Your landlord kept the deposit', 'She finds the law that protects you, drafts a firm letter, and sends it once you say yes.'],
        ['A court date is coming', 'She builds the hearing kit your attorney walks in with — arguments, exhibits and the questions to expect.'],
        ['A contract you don’t understand', 'Clause by clause: what it means for you, what’s risky, and what to ask for instead.'],
        ['A deadline you can’t miss', 'She works out every date and reminds you well before it matters.'],
      ],
      [
        ['A symptom that worries you', 'She checks for warning signs first, asks what a careful clinician would, and prepares it all for a physician’s review.'],
        ['Lab results full of numbers', 'What’s normal, what isn’t, and what to ask — in plain words, reviewed by a doctor.'],
        ['Too many medications', 'She checks every combination for interactions and flags anything to raise with your prescriber.'],
        ['A condition to keep an eye on', 'Weekly check-ins for as long as you need, with a physician watching the trend.'],
      ],
      [
        ['After a car accident', 'Your medical records become a clear claim: injuries, costs, and a demand your attorney signs.'],
        ['Insurance said no', 'A doctor’s letter of medical necessity and a legal appeal, assembled together, before the deadline.'],
        ['Hurt at work', 'Your treatment timeline, the right forms, and every filing date — in one place.'],
        ['Getting your records', 'She writes the request, follows up after 30 days, and files what arrives.'],
      ],
    ],
  },
  how: {
    eyebrow: 'How it works',
    title: 'She does the work. You stay in control.',
    steps: [
      ['Tell her what’s going on', 'Type it, or upload the letter, the bill, the lab report. In English or Chinese.'],
      ['She gets to work — even while you sleep', 'Research, drafts, checks and reminders. She keeps going for days or weeks and picks up exactly where she left off.'],
      ['A licensed professional signs off', 'Court filings, prescriptions, test orders and medical plans are reviewed by an ACT attorney or physician.'],
      ['Nothing leaves without your yes', 'Letters and emails wait for you. You see exactly what will be sent, and approve it first.'],
    ],
    timelineTitle: 'Always know what’s happening',
    timeline: [
      ['Mon 09:02', 'Found three rulings that support your claim'],
      ['Mon 09:15', 'Draft letter ready for your review'],
      ['Tue 10:00', 'You approved it — letter sent'],
      ['Next · Oct 14', 'Response deadline. Vera will remind you the day before.'],
    ],
    trust: [
      ['Real professionals', 'Licensed attorneys and physicians review every step that needs a licence.'],
      ['Your approval, every time', 'Nothing is sent, filed or ordered without the right person saying yes.'],
      ['Private by design', 'Your files are yours. Export or delete everything, whenever you like.'],
      ['Emergencies first', 'Describe an emergency and she tells you to call for help — immediately.'],
    ],
  },
  cta: {
    title: 'Start with a conversation.',
    sub: 'Tell Vera what’s happening. She’ll take it from there — and keep you in the loop.',
    button: 'Talk to Vera',
    secondary: 'I already have an account',
  },
  faq: {
    title: 'Questions, answered honestly',
    items: [
      ['Is Vera a lawyer or a doctor?', 'No. Vera is an AI assistant. She works alongside licensed ACT attorneys and physicians, who review and sign anything that requires a licence.'],
      ['Can Vera go to court for me?', 'A licensed attorney appears in court. Vera prepares everything they bring, and supports them through the hearing with research and notes.'],
      ['Can Vera diagnose me or prescribe medicine?', 'She helps work out what might be going on and prepares a plan, but diagnoses, test orders and prescriptions come from a licensed physician. Controlled medications are never prescribed through ACT.'],
      ['What if it’s an emergency?', 'Call 911 (or 120 in mainland China) right away. If you describe an emergency to Vera, that is the first thing she will tell you.'],
      ['Is my information private?', 'Your conversations and files belong to your account. You can see what Vera remembers, turn memory off, export everything, or delete your account at any time.'],
      ['Does she speak Chinese?', 'Yes — 维拉会说中文. She answers in the language you write in, and the whole site switches with one tap.'],
    ],
  },
  footer: {
    disclaimer: 'ACT provides information and assistance. Nothing here is legal or medical advice until it has been reviewed by a licensed professional. In an emergency, call 911 or your local emergency number.',
    rights: '© 2026 ACT. All rights reserved.',
    links: ['Privacy', 'Terms', 'Contact'],
  },
  auth: {
    signinTitle: 'Welcome back', signinSub: 'Sign in to continue with Vera.',
    signupTitle: 'Create your account', signupSub: 'It takes a minute. Vera will be waiting.',
    email: 'Email', password: 'Password', name: 'Your name', namePh: 'What should Vera call you?',
    newPassword: 'New password', confirm: 'Confirm password',
    pwHint: 'At least 10 characters. A short sentence works well.',
    signin: 'Sign in', signup: 'Create account', forgot: 'Forgot password?',
    noAccount: 'New here?', haveAccount: 'Already have an account?',
    agree: 'By creating an account you agree to the {terms} and {privacy}, and you understand that Vera is an AI assistant and that legal and medical decisions are made by licensed professionals.',
    termsLink: 'Terms of Service', privacyLink: 'Privacy Policy',
    forgotTitle: 'Reset your password', forgotSub: 'Enter your email and we’ll send you a link.', sendLink: 'Send reset link',
    forgotSent: 'If an account exists for that address, a reset link is on its way. It works for one hour.',
    resetTitle: 'Choose a new password', resetSub: 'You’ll be signed in on this device, and signed out everywhere else.', reset: 'Save new password',
    resetDone: 'Password updated.',
    verifyTitle: 'Check your inbox', verifySub: 'We sent a confirmation link to', verifyHelp: 'Open it on any device to finish. It’s valid for 48 hours.',
    resend: 'Send it again', resent: 'Sent. Check your spam folder too.',
    verifying: 'Confirming your email…', verified: 'Email confirmed', verifiedSub: 'You’re all set. Vera is ready when you are.',
    continue: 'Continue', backToSignin: 'Back to sign in', mismatch: 'Passwords don’t match', mailOff: 'Email is not configured on this server yet — ask the administrator for your link.',
    side: ['Your matters, handled with care.', 'Prepared by Vera. Signed off by licensed professionals. Approved by you.'],
    strength: ['Too short', 'Okay', 'Good', 'Strong'],
    signout: 'Sign out',
  },
  app: {
    newChat: 'New conversation', search: 'Search conversations', cases: 'Cases', documents: 'Documents', approvals: 'Approvals', settings: 'Settings',
    chats: 'Conversations', archived: 'Archived', empty: 'No conversations yet',
    greeting: 'Hi {name}, I’m Vera.', greetingSub: 'Tell me what’s going on — a legal problem, a health worry, or both. I’ll take it one step at a time.',
    placeholder: 'Tell Vera what’s happening…', send: 'Send', stop: 'Stop', attach: 'Attach a file', uploading: 'Reading your file…',
    uploaded: 'Added to your file: {name}', thinking: 'Vera is thinking', disclaimer: 'Vera is an AI assistant. Licensed professionals review anything that needs a licence. In an emergency call 911 / 120.',
    suggestions: ['My landlord won’t return my deposit', 'I’ve had a headache for three days', 'Help me understand my lab results', 'I was hurt in a car accident'],
    caseOpened: 'Case opened', viewCase: 'View case', progress: '{done} of {total} steps', rename: 'Rename', archive: 'Archive', unarchive: 'Restore', delete: 'Delete',
    confirmDelete: 'Delete this conversation? Cases and documents stay in your file.',
    verifyBanner: 'Confirm your email to start talking with Vera.', verifyAction: 'Resend link',
    live: 'Live', offline: 'Reconnecting…', copy: 'Copy', copied: 'Copied',
    emergency: 'Emergency guidance shown',
  },
  goal: {
    status: { planning: 'Planning', active: 'In progress', paused: 'Paused', needs_attention: 'Needs you', completed: 'Completed', failed: 'Stopped', cancelled: 'Cancelled' },
    task: { blocked: 'Waiting', ready: 'Up next', leased: 'Working', waiting_approval: 'Needs approval', succeeded: 'Done', failed: 'Failed', cancelled: 'Cancelled', skipped: 'Skipped' },
    domain: { legal: 'Legal', medical: 'Health', medlegal: 'Legal & health', general: 'General' },
    pause: 'Pause', resume: 'Resume', cancel: 'Cancel case', confirmCancel: 'Cancel this case? Unfinished work stops. Anything already sent stays sent.',
    plan: 'Plan', timeline: 'Timeline', files: 'Documents', next: 'What happens next', nothingNext: 'Nothing scheduled.',
    criteria: 'Done when', usage: 'Effort so far', steps: 'steps', tools: 'actions', cost: 'est. cost',
    why: 'Why', attention: 'Vera needs you', none: 'No cases yet. Start a conversation and Vera will open one when there’s real work to do.',
    reminders: 'Reminders', earlier: 'Earlier',
  },
  approval: {
    title: 'Approval needed', approve: 'Approve', decline: 'Decline', note: 'Add a note (optional)',
    g1: 'Needs your approval', g2: 'Needs a licensed {role}', waiting: 'Waiting for a licensed {role} to review',
    approved: 'Approved', rejected: 'Declined', superseded: 'Withdrawn', none: 'Nothing waiting for approval.',
    queue: 'Professional review queue', client: 'Client',
    tools: { email_send: 'Send an email', court_efile: 'File with the court', rx_submit: 'Send a prescription', lab_order: 'Order tests', referral_send: 'Send a referral', request_professional_signoff: 'Professional sign-off' } as Record<string, string>,
  },
  docs: { title: 'Documents', upload: 'Upload', empty: 'No documents yet.', version: 'v{v}', kind: 'Type', from: 'Case', download: 'Download' },
  settings: {
    title: 'Settings',
    tabs: { profile: 'Profile', prefs: 'Vera & language', notify: 'Notifications', privacy: 'Privacy & memory', security: 'Security', limits: 'Limits & usage', pro: 'Professional', look: 'Appearance', admin: 'Admin' },
    save: 'Save changes', saved: 'Saved', name: 'Name', email: 'Email', role: 'Role', joined: 'Member since',
    language: 'Language', tone: 'Vera’s tone', tones: { warm: 'Warm', neutral: 'Neutral', formal: 'Formal' },
    verbosity: 'Answer length', verbosities: { brief: 'Brief', balanced: 'Balanced', detailed: 'Detailed' },
    timezone: 'Time zone', jurisdiction: 'Where you live (state / province)', jurisdictionHint: 'Helps Vera apply the right local rules.',
    notifyEmail: 'Email me reminders and updates', notifyApprovals: 'Email me when something needs my approval',
    memory: 'Let Vera remember facts about me', memoryHint: 'Things like your state, allergies or medications — so you don’t repeat yourself.',
    memories: 'What Vera remembers', forget: 'Forget', forgetAll: 'Forget everything', noMemories: 'Nothing remembered yet.',
    export: 'Export my data', exportHint: 'Everything in your account, as one JSON file.', policy: 'Read the Privacy Policy',
    deleteAccount: 'Delete account', deleteHint: 'Permanently deletes your account, conversations, cases and documents.', deleteConfirm: 'Enter your password to delete everything',
    password: 'Change password', current: 'Current password', next: 'New password', updatePw: 'Update password',
    sessions: 'Signed-in devices', thisDevice: 'This device', revoke: 'Sign out', revokeOthers: 'Sign out everywhere else', lastSeen: 'Last active',
    maxCost: 'Maximum estimated cost per case (USD)', maxDays: 'Maximum length of a case (days)', limitsHint: 'When a case reaches a limit, Vera pauses and asks you before continuing.',
    today: 'Used today', month: 'Used this month', tokens: 'tokens',
    proTitle: 'Are you a licensed attorney or physician?', proSub: 'Add your licence. After an administrator verifies it you can review and approve work in the professional queue.',
    kind: 'Licence', bar: 'Bar admission (attorney)', medical: 'Medical licence (physician)', number: 'Licence number', jur: 'Jurisdiction', submit: 'Submit for verification',
    lic: { pending: 'Pending verification', verified: 'Verified', rejected: 'Rejected' },
    theme: 'Theme', themes: { system: 'System', light: 'Light', dark: 'Dark' }, fontSize: 'Text size', sizes: { small: 'Small', medium: 'Medium', large: 'Large' },
    enterToSend: 'Press Enter to send (Shift+Enter for a new line)', reduceMotion: 'Reduce motion',
    adminTitle: 'Licence verification', verify: 'Verify', reject: 'Reject',
  },
  common: { loading: 'Loading…', error: 'Something went wrong', retry: 'Try again', close: 'Close', back: 'Back', cancel: 'Cancel', confirm: 'Confirm', yes: 'Yes', no: 'No', open: 'Open', language: 'Language' },
}

type Dict = typeof en

const zh: Dict = {
  brand: { name: 'ACT', expand: '维护 · 关怀 · 信任', agent: '维拉' },
  nav: { vera: '认识维拉', what: '她能做什么', how: '如何运作', faq: '常见问题', signin: '登录', start: '开始使用', app: '进入应用' },
  hero: {
    left: '为你辩护', right1: '也为你', right2: '守护',
    tagline: '法庭与诊室之间，一位始终在你身边的伙伴。',
    sub: '维拉帮你准备案件、照看健康，日夜不停——凡是要紧的事，都由持证律师和医生把关签字。',
    meetTitle: '认识维拉', meetSub: '看看是谁在照顾你。',
    focusTitle: '专注于今天最重要的事',
    focusSub: '告诉她发生了什么。她会整理事实、找到保护你的规则，并记住每一个日期。',
    caption: '在你休息时也不停工作的伙伴——支持中文与 English',
    scenes: ['法律', '健康', '两者兼有'],
    cards: [
      {
        rows: [['押金纠纷', '高', '进行中', '3天'], ['催告函', '待你', '确认', '1分']],
        panel: '本周', panelSub: '维拉正在处理',
        items: [['周一 · 09:00', '庭审材料包', '待律师审阅', 'o'], ['周四 · 14:00', '答复期限', '已设提醒', 'y']],
      },
      {
        rows: [['血压管理', '每周', '按计划', '3周'], ['化验结果', '医生', '已审阅', '2天']],
        panel: '你的健康', panelSub: '随访与复核',
        items: [['周二 · 08:30', '血压随访', '第 3 / 4 周', 'o'], ['周五 · 10:00', '用药复核', '医生参与', 'y']],
      },
      {
        rows: [['车祸索赔', '高', '整理病历', '5天'], ['保险申诉', '截止', '10月14日', '3周']],
        panel: '案件与健康', panelSub: '一份档案，两方面都顾到',
        items: [['周三 · 11:00', '医疗摘要', '用于索赔', 'o'], ['10月14日', '申诉截止', '材料已备好', 'y']],
      },
    ],
  },
  meet: {
    eyebrow: '认识维拉 · Vera',
    title: '她的名字，意为“真实”。',
    body: '维拉是一位 AI 伙伴，陪你走过人生中最难的两个场合——法庭与诊室。她先倾听，再用平实的话解释，从不假装自己是别的什么。她不是律师，也不是医生——她与我们的律师和医生一起工作。',
    quote: '“用你自己的话告诉我发生了什么。我们一步一步来。”',
    values: [
      ['真实胜于安慰', '她告诉你可能的结果，而不只是好听的话。'],
      ['安全第一', '只要听起来紧急，她会先说这件事。'],
      ['每一步都有依据', '每封信、每个出处、每个步骤，你都能看到为什么。'],
      ['守住分寸', '凡需执照的事，都交给持证的专业人士。'],
      ['从不忘记日期', '期限、开庭、随访——她都记着，并提前提醒你。'],
    ],
    moods: ['她在倾听时', '她在思考时', '有好消息时'],
    chips: ['中文 & English', '全天候在线', '只记住你允许的内容'],
  },
  what: {
    eyebrow: '她能为你做什么',
    title: '在等不起的时刻，给你帮助。',
    tabs: ['法律难题', '你的健康', '两者兼有'],
    items: [
      [
        ['房东扣着押金不退', '她找到保护你的法律，起草一封有力的催告函，经你同意后再发出。'],
        ['快要开庭了', '她为你的律师准备好庭审材料包——论点、证据，以及可能被问到的问题。'],
        ['看不懂的合同', '逐条说明：对你意味着什么、哪里有风险、应该如何争取。'],
        ['不能错过的期限', '她算出每一个日期，并在关键时刻之前提醒你。'],
      ],
      [
        ['让你担心的症状', '她先排查危险信号，像细心的医生那样问诊，再把一切整理好交给医生审阅。'],
        ['满是数字的化验单', '哪些正常、哪些不正常、该问医生什么——说得明白，并经医生审阅。'],
        ['吃的药太多', '她检查每一种组合是否有相互作用，并标出需要和开药医生讨论的地方。'],
        ['需要长期关注的病情', '按你需要的时长每周随访，由医生关注变化趋势。'],
      ],
      [
        ['车祸之后', '把你的病历整理成清晰的索赔：伤情、费用，以及由律师签字的索赔函。'],
        ['保险拒赔了', '医生的医疗必要性证明加上法律申诉，一起准备，赶在截止日期之前。'],
        ['工作中受伤', '治疗时间线、正确的表格、每一个申报日期——都在一个地方。'],
        ['调取病历', '她写好申请，30 天后跟进，并把收到的资料归档。'],
      ],
    ],
  },
  how: {
    eyebrow: '如何运作',
    title: '工作交给她，决定权在你。',
    steps: [
      ['告诉她发生了什么', '直接输入，或上传信件、账单、化验单。中文英文都可以。'],
      ['她开始工作——即使你在睡觉', '查资料、写草稿、做核对、设提醒。她可以持续几天甚至几周，并总能从上次停下的地方继续。'],
      ['持证专业人士签字把关', '法院文书、处方、检查单和治疗方案，都由 ACT 的律师或医生审阅。'],
      ['没有你的同意，什么都不会发出', '信件和邮件会等你确认。你能看到将要发出的确切内容，同意后才发送。'],
    ],
    timelineTitle: '随时知道进展',
    timeline: [
      ['周一 09:02', '找到三个支持你主张的判例'],
      ['周一 09:15', '催告函草稿已备好，等你审阅'],
      ['周二 10:00', '你已同意——信件已发出'],
      ['下一步 · 10月14日', '答复期限。维拉会在前一天提醒你。'],
    ],
    trust: [
      ['真正的专业人士', '凡需执照的步骤，都由持证律师和医生审阅。'],
      ['每一次都需要你的同意', '没有合适的人点头，任何东西都不会被发送、提交或下单。'],
      ['隐私优先', '你的文件属于你。随时导出或删除全部内容。'],
      ['紧急情况优先', '只要你描述了紧急情况，她会立刻让你拨打急救电话。'],
    ],
  },
  cta: {
    title: '从一次对话开始。',
    sub: '告诉维拉发生了什么。接下来交给她——她会随时让你知道进展。',
    button: '和维拉聊聊',
    secondary: '我已有账户',
  },
  faq: {
    title: '坦诚回答你的问题',
    items: [
      ['维拉是律师或医生吗？', '不是。维拉是 AI 助手。她与 ACT 的持证律师和医生一起工作，凡需执照的事项都由他们审阅并签字。'],
      ['维拉能替我出庭吗？', '出庭的是持证律师。维拉为律师准备好一切材料，并在庭审过程中提供检索和记录支持。'],
      ['维拉能给我诊断或开药吗？', '她可以帮助分析可能的情况并准备方案，但诊断、检查单和处方都来自持证医生。ACT 绝不开具管制类药物。'],
      ['遇到紧急情况怎么办？', '请立即拨打 120（中国大陆）或 911（美国）。只要你向维拉描述紧急情况，她首先就会这样告诉你。'],
      ['我的信息安全吗？', '你的对话和文件只属于你的账户。你可以查看维拉记住了什么、关闭记忆、导出全部数据，或随时删除账户。'],
      ['她会说英文吗？', '会——Vera speaks English. 你用哪种语言写，她就用哪种语言回复；整个网站一键切换。'],
    ],
  },
  footer: {
    disclaimer: 'ACT 提供信息与协助。在持证专业人士审阅之前，本站内容均不构成法律或医疗意见。紧急情况请拨打 120 / 911 或当地急救电话。',
    rights: '© 2026 ACT 保留所有权利。',
    links: ['隐私', '条款', '联系我们'],
  },
  auth: {
    signinTitle: '欢迎回来', signinSub: '登录后继续与维拉对话。',
    signupTitle: '创建账户', signupSub: '只需一分钟，维拉在等你。',
    email: '邮箱', password: '密码', name: '你的名字', namePh: '维拉该怎么称呼你？',
    newPassword: '新密码', confirm: '确认密码',
    pwHint: '至少 10 个字符，一句短句就很好。',
    signin: '登录', signup: '创建账户', forgot: '忘记密码？',
    noAccount: '第一次来？', haveAccount: '已有账户？',
    agree: '创建账户即表示你同意{terms}和{privacy}，并了解维拉是 AI 助手，法律和医疗决定由持证专业人士作出。',
    termsLink: '《服务条款》', privacyLink: '《隐私政策》',
    forgotTitle: '重置密码', forgotSub: '输入你的邮箱，我们会发送重置链接。', sendLink: '发送重置链接',
    forgotSent: '如果该邮箱存在账户，重置链接已发出，一小时内有效。',
    resetTitle: '设置新密码', resetSub: '你将在此设备上登录，并在其他所有设备上退出。', reset: '保存新密码',
    resetDone: '密码已更新。',
    verifyTitle: '请查收邮件', verifySub: '我们已将确认链接发送至', verifyHelp: '在任意设备上打开即可完成，48 小时内有效。',
    resend: '重新发送', resent: '已发送，也请查看垃圾邮件文件夹。',
    verifying: '正在确认你的邮箱…', verified: '邮箱已确认', verifiedSub: '一切就绪，维拉随时为你服务。',
    continue: '继续', backToSignin: '返回登录', mismatch: '两次输入的密码不一致', mailOff: '此服务器尚未配置邮件——请向管理员索取链接。',
    side: ['你的事，被用心对待。', '由维拉准备，经持证专业人士签字，由你最终确认。'],
    strength: ['太短', '一般', '良好', '很强'],
    signout: '退出登录',
  },
  app: {
    newChat: '新对话', search: '搜索对话', cases: '案件', documents: '文件', approvals: '待审批', settings: '设置',
    chats: '对话', archived: '已归档', empty: '还没有对话',
    greeting: '{name}你好，我是维拉。', greetingSub: '告诉我发生了什么——法律问题、健康担忧，或两者都有。我们一步一步来。',
    placeholder: '告诉维拉发生了什么…', send: '发送', stop: '停止', attach: '添加文件', uploading: '正在读取你的文件…',
    uploaded: '已加入你的档案：{name}', thinking: '维拉正在思考', disclaimer: '维拉是 AI 助手，凡需执照的事项由持证专业人士审阅。紧急情况请拨打 120 / 911。',
    suggestions: ['房东不退我的押金', '我头痛已经三天了', '帮我看看化验结果', '我在车祸中受伤了'],
    caseOpened: '已建立案件', viewCase: '查看案件', progress: '{done} / {total} 步', rename: '重命名', archive: '归档', unarchive: '恢复', delete: '删除',
    confirmDelete: '删除此对话？案件和文件仍会保留在你的档案中。',
    verifyBanner: '请先确认邮箱，再开始与维拉对话。', verifyAction: '重新发送链接',
    live: '实时', offline: '正在重新连接…', copy: '复制', copied: '已复制',
    emergency: '已显示紧急指引',
  },
  goal: {
    status: { planning: '规划中', active: '进行中', paused: '已暂停', needs_attention: '需要你', completed: '已完成', failed: '已停止', cancelled: '已取消' },
    task: { blocked: '等待中', ready: '下一步', leased: '处理中', waiting_approval: '待审批', succeeded: '完成', failed: '失败', cancelled: '已取消', skipped: '已跳过' },
    domain: { legal: '法律', medical: '健康', medlegal: '法律与健康', general: '通用' },
    pause: '暂停', resume: '继续', cancel: '取消案件', confirmCancel: '取消此案件？未完成的工作会停止，已发出的内容无法撤回。',
    plan: '计划', timeline: '时间线', files: '文件', next: '接下来', nothingNext: '暂无安排。',
    criteria: '完成标准', usage: '目前投入', steps: '步', tools: '次操作', cost: '估算费用',
    why: '原因', attention: '维拉需要你', none: '还没有案件。开始对话，需要实际工作时维拉会为你建立案件。',
    reminders: '提醒', earlier: '更早',
  },
  approval: {
    title: '需要审批', approve: '同意', decline: '拒绝', note: '添加备注（可选）',
    g1: '需要你的同意', g2: '需要持证{role}审批', waiting: '等待持证{role}审阅',
    approved: '已同意', rejected: '已拒绝', superseded: '已撤回', none: '暂无待审批事项。',
    queue: '专业审阅队列', client: '客户',
    tools: { email_send: '发送邮件', court_efile: '向法院提交', rx_submit: '开具处方', lab_order: '开检查单', referral_send: '转诊', request_professional_signoff: '专业签字' },
  },
  docs: { title: '文件', upload: '上传', empty: '还没有文件。', version: '第{v}版', kind: '类型', from: '案件', download: '下载' },
  settings: {
    title: '设置',
    tabs: { profile: '个人资料', prefs: '维拉与语言', notify: '通知', privacy: '隐私与记忆', security: '安全', limits: '额度与用量', pro: '专业人士', look: '外观', admin: '管理' },
    save: '保存', saved: '已保存', name: '名字', email: '邮箱', role: '身份', joined: '注册时间',
    language: '语言', tone: '维拉的语气', tones: { warm: '温暖', neutral: '中性', formal: '正式' },
    verbosity: '回答长度', verbosities: { brief: '简洁', balanced: '适中', detailed: '详细' },
    timezone: '时区', jurisdiction: '所在地（州 / 省）', jurisdictionHint: '帮助维拉适用正确的当地规则。',
    notifyEmail: '通过邮件接收提醒和进展', notifyApprovals: '有事项需要我审批时发邮件通知',
    memory: '允许维拉记住关于我的信息', memoryHint: '例如你所在的州、过敏史或正在服用的药物——免得你重复说明。',
    memories: '维拉记住的内容', forget: '忘记', forgetAll: '全部忘记', noMemories: '暂无记忆。',
    export: '导出我的数据', exportHint: '账户中的全部内容，打包为一个 JSON 文件。', policy: '阅读《隐私政策》',
    deleteAccount: '删除账户', deleteHint: '永久删除你的账户、对话、案件和文件。', deleteConfirm: '输入密码以删除全部内容',
    password: '修改密码', current: '当前密码', next: '新密码', updatePw: '更新密码',
    sessions: '已登录的设备', thisDevice: '当前设备', revoke: '退出', revokeOthers: '退出其他所有设备', lastSeen: '最近活动',
    maxCost: '每个案件的最高估算费用（美元）', maxDays: '每个案件的最长时长（天）', limitsHint: '案件达到上限时，维拉会暂停并先征求你的意见。',
    today: '今日用量', month: '本月用量', tokens: 'tokens',
    proTitle: '你是持证律师或医生吗？', proSub: '添加你的执照。管理员核验后，你即可在专业队列中审阅并批准工作。',
    kind: '执照类型', bar: '律师执业资格', medical: '医师执照', number: '执照编号', jur: '执业地区', submit: '提交核验',
    lic: { pending: '待核验', verified: '已核验', rejected: '未通过' },
    theme: '主题', themes: { system: '跟随系统', light: '浅色', dark: '深色' }, fontSize: '字号', sizes: { small: '小', medium: '中', large: '大' },
    enterToSend: '按 Enter 发送（Shift+Enter 换行）', reduceMotion: '减少动效',
    adminTitle: '执照核验', verify: '通过', reject: '拒绝',
  },
  common: { loading: '加载中…', error: '出了点问题', retry: '重试', close: '关闭', back: '返回', cancel: '取消', confirm: '确认', yes: '是', no: '否', open: '打开', language: '语言' },
}

const dicts: Record<Lang, Dict> = { en, zh }

type Ctx = { lang: Lang; setLang: (l: Lang) => void; t: Dict; f: (s: string, vars: Record<string, string | number>) => string }
const I18n = createContext<Ctx>(null as unknown as Ctx)

function initialLang(): Lang {
  try {
    const s = localStorage.getItem('act.lang')
    if (s === 'en' || s === 'zh') return s
  } catch {}
  return navigator.language?.toLowerCase().startsWith('zh') ? 'zh' : 'en'
}

export function I18nProvider({ children }: { children: ReactNode }) {
  const [lang, setLangState] = useState<Lang>(initialLang)
  const setLang = (l: Lang) => {
    setLangState(l)
    try {
      localStorage.setItem('act.lang', l)
    } catch {}
  }
  useEffect(() => {
    document.documentElement.lang = lang === 'zh' ? 'zh-CN' : 'en'
  }, [lang])
  const value = useMemo<Ctx>(
    () => ({ lang, setLang, t: dicts[lang], f: (s, vars) => s.replace(/\{(\w+)\}/g, (_, k) => String(vars[k] ?? '')) }),
    [lang],
  )
  return <I18n.Provider value={value}>{children}</I18n.Provider>
}

export const useI18n = () => useContext(I18n)
