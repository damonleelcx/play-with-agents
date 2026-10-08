import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'

export type Lang = 'en' | 'zh' | 'ko' | 'ja'
export const LANGS: readonly Lang[] = ['en', 'zh', 'ko', 'ja']
export const isLang = (v: unknown): v is Lang => typeof v === 'string' && (LANGS as readonly string[]).includes(v)

// How each language names itself, and the short mark used where space is tight.
// These are never translated: a reader looks for their own language's name.
export const LANG_NAMES: Record<Lang, { native: string; short: string; english: string }> = {
  en: { native: 'English', short: 'EN', english: 'English' },
  zh: { native: '中文', short: '中', english: 'Chinese' },
  ko: { native: '한국어', short: '한', english: 'Korean' },
  ja: { native: '日本語', short: '日', english: 'Japanese' },
}

// BCP 47 tags: <html lang> and every Intl formatter.
export const LOCALES: Record<Lang, string> = { en: 'en', zh: 'zh-CN', ko: 'ko', ja: 'ja' }
export const localeOf = (lang: string) => LOCALES[isLang(lang) ? lang : 'en']

// Every user-facing string of the signed-in app, auth and legal pages lives
// here, in all four languages. (The landing and the play surfaces keep their
// own copy in pages/landing/strings.ts and app/play/strings.ts.)
// ko and ja are typed as `Dict`, so a key missing there is a compile error;
// at run time any gap still falls back to English, never to the key.
const en = {
  auth: {
    signinTitle: 'Welcome back', signinSub: 'Your seat is still warm. Sign in to pick up where you left off.',
    signupTitle: 'Take a seat', signupSub: 'One minute to sign up. Then the cards are dealt.',
    email: 'Email', password: 'Password', name: 'Your name', namePh: 'What should Aoi call you?',
    newPassword: 'New password', confirm: 'Confirm password',
    pwHint: 'At least 10 characters. A short sentence works well.',
    pwTips: ['10+ characters', 'Upper and lower case', 'A number or symbol'],
    show: 'Show', hide: 'Hide', showPw: 'Show password', hidePw: 'Hide password',
    signin: 'Sign in', signup: 'Create account', forgot: 'Forgot password?',
    noAccount: 'New here?', haveAccount: 'Already have an account?',
    agree: 'By creating an account you agree to the {terms} and {privacy}. Chips are play money; the agents are AIs.',
    termsLink: 'Terms', privacyLink: 'Privacy Policy',
    forgotTitle: 'Lost your password?', forgotSub: 'Enter your email and we’ll send you a link to choose a new one.', sendLink: 'Send reset link',
    forgotSent: 'If an account exists for that address, a reset link is on its way. It works for one hour.',
    resetTitle: 'Choose a new password', resetSub: 'You’ll be signed in here, and signed out everywhere else.', reset: 'Save new password',
    resetDone: 'Password updated.', linkInvalid: 'This link is invalid or has expired.',
    verifyTitle: 'Check your inbox', verifySub: 'We sent a confirmation link to', verifyHelp: 'Open it on any device. This page moves on by itself once you have.',
    resend: 'Send it again', resent: 'Sent. Check your spam folder too.', waiting: 'Waiting for you to click the link…',
    verifying: 'Confirming your email…', verified: 'You’re in', verifiedSub: 'Email confirmed. Aoi has a seat ready for you.', confirmed: 'Email confirmed', confirmedSub: 'Email confirmed — sign in to continue.',
    continue: 'Let’s play', backToSignin: 'Back to sign in', mismatch: 'Passwords don’t match', mailOff: 'Email is not configured on this server yet. Ask the administrator for your link.',
    badCreds: 'That email and password don’t match.', offline: 'Can’t reach the server. Check your connection and try again.',
    strength: ['Too short', 'Okay', 'Good', 'Strong'],
    signout: 'Sign out', useOther: 'Use another account',
    lines: {
      signin: '“Back for a rematch? I kept your seat warm.”',
      signup: '“A new player! I’ll go easy on you. For one hand.”',
      verify: '“One click on that email and we’re dealing.”',
      forgot: '“Happens to the best of us. Even Ren forgets things. Rarely.”',
      reset: '“New password, new luck. Let’s make it a strong one.”',
    },
    tagline: 'Aoi 葵 · your host at every table',
    // Server errors arrive in English; these are shown instead when known.
    serverErrors: {} as Record<string, string>,
  },
  app: {
    brand: 'Play with Agents',
    chat: 'Aoi', play: 'Play', studio: 'Studio', settings: 'Settings', chats: 'Chats',
    newChat: 'New chat', search: 'Search chats', archived: 'Archived', showArchived: 'Show archived', hideArchived: 'Hide archived',
    empty: 'No chats yet. Say hi to Aoi.', noMatch: 'No chats match.',
    rename: 'Rename', archive: 'Archive', unarchive: 'Restore', delete: 'Delete',
    confirmDelete: 'Delete this chat for good? Tables and games it started stay yours.',
    collapse: 'Collapse sidebar', expand: 'Expand sidebar', menu: 'Menu', more: 'More',
    theme: 'Theme', dark: 'Dark', light: 'Light', language: 'Language', signout: 'Sign out', profile: 'Profile',
    live: 'Live', offline: 'Reconnecting…', untitled: 'New chat',
  },
  chat: {
    hello: 'Hi {name}! I’m Aoi.', helloAnon: 'Hi! I’m Aoi.',
    intro: 'I host the tables here. I can deal you into Texas Hold’em with my friends, teach you the ropes, or build a brand-new game with you. What are we playing?',
    suggestions: ['Deal me into Hold’em with Mika and Ren', 'Teach me poker in 2 minutes', 'Let’s build a game together', 'Who are the other agents?'],
    placeholder: 'Message Aoi…', send: 'Send', stop: 'Stop', thinking: 'Aoi is thinking', typing: 'Aoi is typing',
    hint: 'Enter to send · Shift+Enter for a new line',
    note: 'Aoi is an AI. Chips are play money.',
    failed: 'That didn’t go through.', retry: 'Retry', copy: 'Copy', copied: 'Copied',
    offline: 'Aoi can’t be reached right now. Your message is safe; try again in a moment.',
    speak: 'Play Aoi’s voice', stopSpeaking: 'Stop', enableVoice: 'Tap to hear Aoi’s voice', speaking: 'Aoi is speaking',
    role: 'Your host · AI agent', you: 'You', jump: 'Jump to latest', loadError: 'Couldn’t load this chat.',
  },
  cards: {
    mission: 'Studio mission', game: 'Game', table: 'Table',
    viewMission: 'Open mission', play: 'Play', publishQ: 'Publish it?', publishHint: 'The agents are done. Publish {name} so others can find it?',
    publish: 'Publish', notYet: 'Not yet', published: 'Published', declined: 'Kept as a draft',
    seats: '{min}–{max} players', seatsOne: '{n} players', plays: '{n} plays', loading: 'Loading…', unavailable: 'Not available right now.',
    planning: 'Planning the build…', step: '{done} of {total} steps',
  },
  steps: { design: 'Design the rules', build: 'Build the game module', cover: 'Illustrate the cover', playtest: 'Playtest hundreds of games', review: 'Independent review', publish: 'Ask the owner to publish', revise: 'Revise the module (round {n})', playtestAgain: 'Playtest again (round {n})', reviewAgain: 'Review again (round {n})' },
  roles: {
    designer: 'Designer', engineer: 'Engineer', playtester: 'Playtester', artist: 'Artist', critic: 'Critic', coordinator: 'Aoi',
    doing: {
      designer: 'Writing the rules', engineer: 'Building the game module', playtester: 'Playtesting hundreds of games', artist: 'Painting the cover', critic: 'Reviewing rules vs build', coordinator: 'Coordinating the team',
    },
  },
  mission: {
    status: { planning: 'Planning', active: 'Building', paused: 'Paused', needs_attention: 'Needs you', completed: 'Done', failed: 'Stopped', cancelled: 'Cancelled' },
    task: { blocked: 'Blocked', ready: 'Ready', leased: 'Running', running: 'Running', waiting_approval: 'Needs you', succeeded: 'Done', done: 'Done', failed: 'Failed', retrying: 'Retrying', cancelled: 'Cancelled', skipped: 'Skipped' } as Record<string, string>,
    pause: 'Pause', resume: 'Resume', cancel: 'Cancel', confirmCancel: 'Cancel this build? Unfinished work stops; drafts already saved stay yours.',
    lanes: 'The team', steps: 'Build steps', timeline: 'What happened', report: 'Playtest report', critic: 'Critic findings',
    approvals: 'Your decision', next: 'What’s next', nothingNext: 'Nothing scheduled.', earlier: 'Earlier',
    attention: 'Aoi needs you', back: 'Studio', chat: 'Chat with Aoi', playDraft: 'Play the draft', more: 'Show all', less: 'Show less',
    noReport: 'The playtester hasn’t reported yet.', noCritic: 'The critic hasn’t weighed in yet.', noEvents: 'Nothing has happened yet.',
    idle: 'Waiting', working: 'Working', done: 'Done', why: 'Why', changed: 'What changed', attempt: 'attempt {n}',
    usage: 'Effort', calls: 'actions', cost: 'est. cost', notFound: 'This mission doesn’t exist or isn’t yours.',
    events: {
      'goal.created': 'Mission started', 'goal.planned': 'Plan made', 'goal.replanned': 'Plan adjusted', 'goal.status': 'Status changed',
      'task.started': 'Started', 'task.resumed': 'Resumed from a checkpoint', 'task.succeeded': 'Finished', 'task.verified': 'Checked and verified',
      'task.verify_failed': 'A check failed: fixing it', 'task.retry': 'Will retry', 'task.failed': 'Failed', 'task.released': 'Paused safely',
      'task.lease_lost': 'Handed over', 'lease.reclaimed': 'Recovered after an interruption', 'tool.called': 'Used a tool',
      'approval.requested': 'Asked for your approval', 'approval.decided': 'You decided', 'budget.exceeded': 'Limit reached',
    } as Record<string, string>,
  },
  studio: {
    title: 'Studio', sub: 'Describe a game. Aoi’s team designs it, builds it, playtests it and sets it on the table.',
    heroA: 'Dream it. ', heroEm: 'They’ll build it.',
    newGame: 'New game', mine: 'My games', workshop: 'In the workshop', noGames: 'No games yet. Your first one is a sentence away.',
    noMissions: 'Nothing on the workbench right now.', play: 'Play', revise: 'Revise', visibility: 'Visibility',
    vis: { private: 'Private', unlisted: 'Unlisted', public: 'Public' } as Record<string, string>,
    status: { building: 'Building', draft: 'Draft', published: 'Published' } as Record<string, string>,
    reviseText: 'Let’s revise {name}: ', starter: 'Let’s build a game where ',
    dialogTitle: 'What should we build?', dialogSub: 'A sentence or two is enough: the theme, how you win, anything you love.',
    dialogPh: 'A pirate dice game for 2–4 players where you bluff about your treasure…',
    build: 'Start the build', talk: 'Talk it through with Aoi', building: 'Starting…', offline: 'The studio is offline. Try again soon, or describe it to Aoi in chat.',
    open: 'Open', loadError: 'Couldn’t load the studio.',
  },
  design: {
    badge: 'Design', title: 'Game design with Aoi', role: 'Designing a game with you', start: 'Design with Aoi',
    startSub: 'Explore new game ideas together, and build when you love it.',
    emptyTitle: 'Let’s invent a game nobody has played yet',
    emptyIntro: 'Give me a theme, a feeling or a mechanic you love. I’ll pitch directions, poke holes and keep our notes on the side. When it clicks, I’ll write the build plan.',
    suggestions: ['Surprise me: three wild game ideas', 'A cozy 2-player game for a rainy evening', 'Twist a classic: chess, but…', 'A bluffing game where the board lies'],
    placeholder: 'Pitch an idea, or ask “what if…”', notes: 'Design notes', notesEmpty: 'Our notes appear here as we design.',
    f: { pitch: 'Pitch', players: 'Players', length: 'Length', board: 'Board & table', components: 'Components', loop: 'A turn', mechanics: 'Mechanics', twist: 'The twist', win: 'Winning', open_questions: 'Still open', parked: 'Parked ideas' },
    readiness: 'Ready to build', makePlan: 'Make the build plan', making: 'Writing the plan…', updatePlan: 'Update the plan',
    stale: 'The design changed after this plan was written.', plan: 'Build plan', build: 'Build this game', building: 'Starting the build…',
    keep: 'Keep designing', built: 'The studio is building it', openMission: 'Open the mission', showNotes: 'Notes', hideNotes: 'Hide notes',
    planFailed: 'The plan could not be written: {e}', buildFailed: 'The build could not start: {e}', notReady: 'A little more design first? The plan works best once the rules are mostly decided.',
  },
  settings: {
    title: 'Settings', saved: 'Saved', saveFailed: 'Couldn’t save: {e}', save: 'Save',
    groups: { profile: 'Profile', aoi: 'Aoi', table: 'Table', agents: 'Agents', studio: 'Studio', notifications: 'Notifications', appearance: 'Appearance', privacy: 'Privacy & memory', security: 'Security', usage: 'Usage & limits' } as Record<string, string>,
    hints: {
      profile: 'How you appear at the table.', aoi: 'How Aoi talks with you.', table: 'How the tables look, sound and move.',
      agents: 'Who sits with you, and how hard they play.', studio: 'Defaults for the games you build.',
      notifications: 'What we email you about.', appearance: 'Theme and text size.', privacy: 'What Aoi remembers, and your data.',
      security: 'Password and signed-in devices.', usage: 'What you’ve used.',
    } as Record<string, string>,
    displayName: 'Display name', displayHint: 'Shown to other players at your tables.', email: 'Email', joined: 'Joined', language: 'Language',
    languageHint: 'Aoi replies and emails you in this language.',
    aoiTone: 'Her tone', tones: { playful: 'Playful', calm: 'Calm', competitive: 'Competitive' },
    previews: { playful: '“Ooh, a raise? Brave. I like brave.”', calm: '“Take your time. The pot will wait.”', competitive: '“Call. Show me what you’ve got.”' } as Record<string, string>,
    aoiTalk: 'How much she talks', talks: { chatty: 'Chatty', normal: 'Normal', quiet: 'Quiet' },
    coaching: 'Coach me during my hands', coachingHint: 'Aoi whispers tips while it’s your turn. Only you see them.',
    callMe: 'Call me', callMeHint: 'A nickname Aoi uses for you.', callMePh: 'e.g. Captain',
    voice: 'Aoi’s voice', voiceOn: 'Let Aoi speak', voiceHint: 'A speaker button on her messages.', autoplay: 'Read replies aloud automatically', autoplayHint: 'New replies only, once they finish.',
    tableVoice: 'Aoi speaks at tables', tableVoiceHint: 'Her table talk, out loud.', volume: 'Volume', hear: 'Hear Aoi',
    sample: 'Hi, I’m Aoi! Pull up a chair — the cards are about to be dealt. 你好，我是葵！',
    memory: 'Let Aoi remember things about me', memoryHint: 'Your favourite games, your style, the nickname you like.',
    turnClock: 'Turn clock', turnHint: 'Time per move at tables you host.', secs: '{n}s', noClock: 'None',
    noClockHint: 'No clock is only used at tables without other humans.',
    agentSpeed: 'Agent speed', speeds: { fast: 'Fast', natural: 'Natural', slow: 'Slow' },
    tableTalk: 'Table talk', talkHint: 'How often agents chat on their own. Off still lets them answer when you talk to them (by name, @, or a reply).', talkModes: { all: 'All', quiet: 'Quiet', off: 'Off' },
    fourColor: 'Four-colour deck', fourColorHint: 'Blue diamonds, green clubs.', handStrength: 'Show my hand strength',
    sound: 'Sound', motion: 'Motion', motions: { full: 'Full', reduced: 'Reduced' },
    cardBack: 'Card back', backs: { aoi: 'Aoi', classic: 'Classic', midnight: 'Midnight' },
    felt: 'Felt', felts: { navy: 'Navy', emerald: 'Emerald', crimson: 'Crimson' },
    difficulty: 'Agent difficulty', difficulties: { casual: 'Casual', regular: 'Regular', shark: 'Shark' },
    fill: 'Fill empty seats with agents', fillHint: 'When a friend doesn’t show, an agent sits in.',
    favourites: 'Favourite agents', favouritesHint: 'Aoi invites these first.',
    studioVis: 'New games are', playtests: 'Playtest games per build', playtestHint: 'More games, sharper balance, slower builds.',
    emailInvites: 'Someone invites me to a table', emailTurn: 'It’s my turn and I’m away', emailBuild: 'A game I asked for is ready',
    theme: 'Theme', themes: { dark: 'Dark', light: 'Light', system: 'System' }, fontSize: 'Text size', sizes: { small: 'Small', medium: 'Medium', large: 'Large' },
    memories: 'What Aoi remembers', forget: 'Forget', forgetAll: 'Forget everything', noMemories: 'Nothing yet.', confirmForgetAll: 'Forget everything Aoi remembers about you?',
    export: 'Export my data', exportHint: 'Your account, games, chats and mission history in one JSON file.', policy: 'Privacy Policy',
    deleteAccount: 'Delete account', deleteHint: 'Permanently deletes your account, chats, tables and games. This can’t be undone.', deleteConfirm: 'Your password',
    password: 'Change password', current: 'Current password', next: 'New password', updatePw: 'Update password',
    sessions: 'Signed-in devices', thisDevice: 'This device', revoke: 'Sign out', revokeOthers: 'Sign out all other devices', lastSeen: 'last seen',
    today: 'Today', month: 'This month', tokens: 'tokens', usageHint: 'Aoi and the studio agents run on language models. This is what you used.',
    missions: 'Recent missions', maxCost: 'Max estimated cost per build (USD)', maxDays: 'Max days per build', limitsHint: 'When a build reaches a limit, it pauses and Aoi asks you first.',
    offline: 'Settings can’t be loaded right now.',
  },
  legal: { home: 'Back to home', contents: 'Contents', note: 'Play money only. No purchase, no cash value, no gambling. The agents are AIs.' },
  common: {
    loading: 'Loading…', error: 'Something went wrong', retry: 'Try again', close: 'Close', back: 'Back', cancel: 'Cancel', confirm: 'Confirm', yes: 'Yes', no: 'No', open: 'Open', language: 'Language', offline: 'Offline', save: 'Save', done: 'Done',
    ago: { now: 'just now', m: '{n}m ago', h: '{n}h ago', d: '{n}d ago' },
  },
}

export type Dict = typeof en

const zh: Dict = {
  auth: {
    signinTitle: '欢迎回来', signinSub: '你的座位还热着。登录，接着上次的牌局。',
    signupTitle: '请入座', signupSub: '一分钟注册，马上发牌。',
    email: '邮箱', password: '密码', name: '你的名字', namePh: '葵该怎么称呼你？',
    newPassword: '新密码', confirm: '确认密码',
    pwHint: '至少 10 个字符。一句短句就很好。',
    pwTips: ['10 个字符以上', '大小写字母', '数字或符号'],
    show: '显示', hide: '隐藏', showPw: '显示密码', hidePw: '隐藏密码',
    signin: '登录', signup: '创建账户', forgot: '忘记密码？',
    noAccount: '第一次来？', haveAccount: '已有账户？',
    agree: '创建账户即表示你同意{terms}和{privacy}。筹码仅为游戏币；所有智能体都是 AI。',
    termsLink: '服务条款', privacyLink: '隐私政策',
    forgotTitle: '忘记密码了？', forgotSub: '输入邮箱，我们会发送一个重设密码的链接。', sendLink: '发送重设链接',
    forgotSent: '如果该邮箱已注册，重设链接正在路上，一小时内有效。',
    resetTitle: '设置新密码', resetSub: '你将在此设备登录，其他设备会被退出。', reset: '保存新密码',
    resetDone: '密码已更新。', linkInvalid: '此链接无效或已过期。',
    verifyTitle: '请查收邮件', verifySub: '我们已发送确认链接至', verifyHelp: '在任意设备上打开即可。点击后，本页会自动继续。',
    resend: '重新发送', resent: '已发送。也请看看垃圾邮件。', waiting: '正在等你点击链接…',
    verifying: '正在确认邮箱…', verified: '欢迎入局', verifiedSub: '邮箱已确认。葵已经给你留好了座位。', confirmed: '邮箱已确认', confirmedSub: '邮箱已确认——请登录后继续。',
    continue: '开始玩', backToSignin: '返回登录', mismatch: '两次输入的密码不一致', mailOff: '服务器尚未配置邮件，请联系管理员获取链接。',
    badCreds: '邮箱或密码不正确。', offline: '无法连接服务器，请检查网络后重试。',
    strength: ['太短', '一般', '不错', '很强'],
    signout: '退出登录', useOther: '使用其他账户',
    lines: {
      signin: '「回来复仇吗？你的位置我一直留着。」',
      signup: '「新玩家！我会手下留情的——就一手。」',
      verify: '「点一下邮件里的链接，我们就开牌。」',
      forgot: '「谁都会忘的。连蓮偶尔也会。」',
      reset: '「新密码，新运气。来个强一点的吧。」',
    },
    tagline: '葵 Aoi · 每张牌桌的主持人',
    serverErrors: {
      'an account with this email already exists': '该邮箱已注册账户',
      'that email address does not look valid': '邮箱地址格式不正确',
      'password must be at least 10 characters': '密码至少需要 10 个字符',
      'password must not be your email address': '密码不能与邮箱相同',
    },
  },
  app: {
    brand: 'Play with Agents',
    chat: '葵', play: '牌桌', studio: '工作室', settings: '设置', chats: '对话',
    newChat: '新对话', search: '搜索对话', archived: '已归档', showArchived: '查看归档', hideArchived: '隐藏归档',
    empty: '还没有对话。和葵打个招呼吧。', noMatch: '没有匹配的对话。',
    rename: '重命名', archive: '归档', unarchive: '恢复', delete: '删除',
    confirmDelete: '永久删除这段对话？由它开始的牌桌和游戏仍归你所有。',
    collapse: '收起侧栏', expand: '展开侧栏', menu: '菜单', more: '更多',
    theme: '主题', dark: '深色', light: '浅色', language: '语言', signout: '退出登录', profile: '个人资料',
    live: '在线', offline: '重新连接中…', untitled: '新对话',
  },
  chat: {
    hello: '{name}，你好！我是葵。', helloAnon: '你好！我是葵。',
    intro: '我是这里的牌桌主持人。可以带你和朋友们打一局德州扑克，手把手教你规则，或者和你一起做一款全新的游戏。今天玩什么？',
    suggestions: ['发牌！我要和美香、蓮打德州', '两分钟教会我德州扑克', '我们一起做个游戏吧', '其他智能体都是谁？'],
    placeholder: '给葵发消息…', send: '发送', stop: '停止', thinking: '葵正在思考', typing: '葵正在输入',
    hint: 'Enter 发送 · Shift+Enter 换行',
    note: '葵是 AI。筹码仅为游戏币。',
    failed: '消息没有发出去。', retry: '重试', copy: '复制', copied: '已复制',
    offline: '暂时联系不上葵。你的消息还在，稍后再试。',
    speak: '播放葵的语音', stopSpeaking: '停止', enableVoice: '点一下，听葵说话', speaking: '葵正在说话',
    role: '你的主持人 · AI 智能体', you: '你', jump: '回到最新', loadError: '无法加载这段对话。',
  },
  cards: {
    mission: '工作室任务', game: '游戏', table: '牌桌',
    viewMission: '查看任务', play: '开玩', publishQ: '发布吗？', publishHint: '智能体们已完成。发布「{name}」让大家都能找到？',
    publish: '发布', notYet: '暂不', published: '已发布', declined: '保留为草稿',
    seats: '{min}–{max} 人', seatsOne: '{n} 人', plays: '{n} 局', loading: '加载中…', unavailable: '暂时无法获取。',
    planning: '正在规划…', step: '第 {done} / {total} 步',
  },
  steps: { design: '设计规则', build: '编写游戏模块', cover: '绘制封面', playtest: '模拟试玩数百局', review: '独立评审', publish: '请主人决定是否发布', revise: '修改模块（第 {n} 轮）', playtestAgain: '再次试玩（第 {n} 轮）', reviewAgain: '再次评审（第 {n} 轮）' },
  roles: {
    designer: '设计师', engineer: '工程师', playtester: '试玩员', artist: '画师', critic: '评审', coordinator: '葵',
    doing: {
      designer: '撰写规则', engineer: '编写游戏模块', playtester: '模拟试玩数百局', artist: '正在绘制封面', critic: '对照规则审查实现', coordinator: '协调团队',
    },
  },
  mission: {
    status: { planning: '规划中', active: '制作中', paused: '已暂停', needs_attention: '需要你', completed: '已完成', failed: '已停止', cancelled: '已取消' },
    task: { blocked: '受阻', ready: '就绪', leased: '进行中', running: '进行中', waiting_approval: '需要你', succeeded: '完成', done: '完成', failed: '失败', retrying: '重试中', cancelled: '已取消', skipped: '已跳过' } as Record<string, string>,
    pause: '暂停', resume: '继续', cancel: '取消', confirmCancel: '取消这次制作？未完成的工作会停止，已保存的草稿仍归你。',
    lanes: '团队', steps: '制作步骤', timeline: '进展记录', report: '试玩报告', critic: '评审意见',
    approvals: '需要你决定', next: '接下来', nothingNext: '暂无安排。', earlier: '更早',
    attention: '葵需要你', back: '工作室', chat: '和葵聊聊', playDraft: '试玩草稿', more: '展开全部', less: '收起',
    noReport: '试玩员还没有提交报告。', noCritic: '评审还没有给出意见。', noEvents: '还没有任何进展。',
    idle: '等待中', working: '工作中', done: '完成', why: '原因', changed: '变化', attempt: '第 {n} 次尝试',
    usage: '工作量', calls: '次操作', cost: '估算费用', notFound: '该任务不存在或不属于你。',
    events: {
      'goal.created': '任务开始', 'goal.planned': '已制定计划', 'goal.replanned': '计划已调整', 'goal.status': '状态变更',
      'task.started': '开始', 'task.resumed': '从检查点继续', 'task.succeeded': '完成', 'task.verified': '已核验',
      'task.verify_failed': '检查未通过，正在修正', 'task.retry': '将重试', 'task.failed': '失败', 'task.released': '已安全暂停',
      'task.lease_lost': '已交接', 'lease.reclaimed': '中断后已恢复', 'tool.called': '使用了工具',
      'approval.requested': '请求你的批准', 'approval.decided': '你已决定', 'budget.exceeded': '达到上限',
    } as Record<string, string>,
  },
  studio: {
    title: '工作室', sub: '描述一款游戏。葵的团队会设计、编写、试玩，然后把它摆上牌桌。',
    heroA: '把想法', heroEm: '变成游戏',
    newGame: '新游戏', mine: '我的游戏', workshop: '制作中', noGames: '还没有游戏。第一款只差一句话。',
    noMissions: '工作台上暂时没有任务。', play: '开玩', revise: '修改', visibility: '可见性',
    vis: { private: '私密', unlisted: '仅链接', public: '公开' } as Record<string, string>,
    status: { building: '制作中', draft: '草稿', published: '已发布' } as Record<string, string>,
    reviseText: '我们来修改「{name}」：', starter: '我们来做一个游戏：',
    dialogTitle: '想做什么游戏？', dialogSub: '一两句话就够：主题、怎么获胜、你喜欢的玩法。',
    dialogPh: '一个 2–4 人的海盗骰子游戏，大家可以对自己的宝藏虚张声势…',
    build: '开始制作', talk: '和葵慢慢聊', building: '启动中…', offline: '工作室暂时离线。稍后再试，或在对话里告诉葵。',
    open: '打开', loadError: '无法加载工作室。',
  },
  design: {
    badge: '设计', title: '和葵一起设计游戏', role: '正在和你一起设计游戏', start: '和葵一起设计',
    startSub: '一起探索全新的玩法，满意了再动手做。',
    emptyTitle: '来发明一款谁都没玩过的游戏吧',
    emptyIntro: '告诉我一个主题、一种感觉，或者你喜欢的机制。我会提出几个方向、挑挑毛病，并在旁边记好设计笔记。等灵感到位，我就写出制作计划。',
    suggestions: ['给我三个脑洞大开的点子', '适合雨夜的温馨双人游戏', '改编经典：像国际象棋，但是……', '棋盘本身会说谎的诈唬游戏'],
    placeholder: '说个点子，或者问“如果……会怎样”', notes: '设计笔记', notesEmpty: '我们边聊，笔记会出现在这里。',
    f: { pitch: '一句话', players: '人数', length: '时长', board: '棋盘与桌面', components: '配件', loop: '一个回合', mechanics: '核心机制', twist: '新意', win: '胜利条件', open_questions: '待定', parked: '暂放的点子' },
    readiness: '可制作程度', makePlan: '生成制作计划', making: '正在写计划……', updatePlan: '更新计划',
    stale: '写完计划之后，设计又有了变化。', plan: '制作计划', build: '开始制作这个游戏', building: '正在启动制作……',
    keep: '继续设计', built: '工作室正在制作', openMission: '查看制作进度', showNotes: '笔记', hideNotes: '收起笔记',
    planFailed: '计划没写成：{e}', buildFailed: '制作没能启动：{e}', notReady: '要不再多设计一点？规则大致定下来以后，计划会更扎实。',
  },
  settings: {
    title: '设置', saved: '已保存', saveFailed: '保存失败：{e}', save: '保存',
    groups: { profile: '个人资料', aoi: '葵', table: '牌桌', agents: '智能体', studio: '工作室', notifications: '通知', appearance: '外观', privacy: '隐私与记忆', security: '安全', usage: '用量与限额' } as Record<string, string>,
    hints: {
      profile: '你在牌桌上的样子。', aoi: '葵和你说话的方式。', table: '牌桌的外观、声音与动效。',
      agents: '谁和你同桌，以及他们有多认真。', studio: '你制作游戏的默认设置。',
      notifications: '我们会通过邮件告诉你什么。', appearance: '主题与字号。', privacy: '葵记得什么，以及你的数据。',
      security: '密码与已登录设备。', usage: '你的使用情况。',
    } as Record<string, string>,
    displayName: '显示名称', displayHint: '同桌的其他玩家会看到。', email: '邮箱', joined: '加入时间', language: '语言',
    languageHint: '葵会用这种语言回复你、给你发邮件。',
    aoiTone: '她的语气', tones: { playful: '俏皮', calm: '沉稳', competitive: '好胜' },
    previews: { playful: '「加注？好大胆，我喜欢。」', calm: '「慢慢来，底池不会跑。」', competitive: '「跟。亮出你的底牌吧。」' },
    aoiTalk: '她说话多少', talks: { chatty: '话多', normal: '适中', quiet: '安静' },
    coaching: '我打牌时给我指点', coachingHint: '轮到你时，葵会悄悄给你提示，只有你能看到。',
    callMe: '称呼我', callMeHint: '葵对你的昵称。', callMePh: '例如：船长',
    voice: '葵的语音', voiceOn: '开启葵的语音', voiceHint: '在她的消息旁显示播放按钮。', autoplay: '自动朗读回复', autoplayHint: '只读新的回复，在回复完成后。',
    tableVoice: '葵在牌桌上说话', tableVoiceHint: '把她的牌桌聊天读出来。', volume: '音量', hear: '听听葵',
    sample: '你好，我是葵！快坐下，马上就要发牌啦。Nice to meet you!',
    memory: '允许葵记住关于我的事', memoryHint: '你喜欢的游戏、你的打法、你喜欢的称呼。',
    turnClock: '行动计时', turnHint: '你开的牌桌每步的时间。', secs: '{n} 秒', noClock: '不计时',
    noClockHint: '只在没有其他真人玩家的牌桌上生效。',
    agentSpeed: '智能体速度', speeds: { fast: '快', natural: '自然', slow: '慢' },
    tableTalk: '牌桌聊天', talkHint: '智能体主动聊天的频率。选“关闭”时，你点名、@ 或回复它们，它们仍会回答。', talkModes: { all: '全部', quiet: '少量', off: '关闭' },
    fourColor: '四色牌', fourColorHint: '蓝色方块、绿色梅花。', handStrength: '显示我的牌力',
    sound: '音效', motion: '动效', motions: { full: '完整', reduced: '减少' },
    cardBack: '牌背', backs: { aoi: '葵', classic: '经典', midnight: '午夜' },
    felt: '桌布', felts: { navy: '藏青', emerald: '翡翠', crimson: '绯红' },
    difficulty: '智能体难度', difficulties: { casual: '休闲', regular: '常规', shark: '高手' },
    fill: '用智能体补满空位', fillHint: '朋友没来时，由智能体入座。',
    favourites: '喜欢的智能体', favouritesHint: '葵会优先邀请他们。',
    studioVis: '新游戏默认', playtests: '每次制作的试玩局数', playtestHint: '局数越多，平衡越好，制作越慢。',
    emailInvites: '有人邀请我入桌', emailTurn: '轮到我但我不在', emailBuild: '我要的游戏做好了',
    theme: '主题', themes: { dark: '深色', light: '浅色', system: '跟随系统' }, fontSize: '字号', sizes: { small: '小', medium: '中', large: '大' },
    memories: '葵记得的事', forget: '忘记', forgetAll: '全部忘记', noMemories: '暂时没有。', confirmForgetAll: '让葵忘记关于你的一切？',
    export: '导出我的数据', exportHint: '账户、游戏、对话和任务记录，打包成一个 JSON 文件。', policy: '隐私政策',
    deleteAccount: '删除账户', deleteHint: '永久删除你的账户、对话、牌桌和游戏，无法恢复。', deleteConfirm: '你的密码',
    password: '修改密码', current: '当前密码', next: '新密码', updatePw: '更新密码',
    sessions: '已登录的设备', thisDevice: '当前设备', revoke: '退出', revokeOthers: '退出其他所有设备', lastSeen: '最近活动',
    today: '今日', month: '本月', tokens: 'tokens', usageHint: '葵和工作室的智能体由语言模型驱动。以下是你的用量。',
    missions: '最近的任务', maxCost: '每次制作的最高估算费用（美元）', maxDays: '每次制作的最长天数', limitsHint: '制作达到上限时会暂停，葵会先问你。',
    offline: '暂时无法加载设置。',
  },
  legal: { home: '返回首页', contents: '目录', note: '仅限游戏币。无需购买，没有现金价值，不涉及赌博。智能体均为 AI。' },
  common: {
    loading: '加载中…', error: '出了点问题', retry: '重试', close: '关闭', back: '返回', cancel: '取消', confirm: '确认', yes: '是', no: '否', open: '打开', language: '语言', offline: '离线', save: '保存', done: '完成',
    ago: { now: '刚刚', m: '{n} 分钟前', h: '{n} 小时前', d: '{n} 天前' },
  },
}

const ko: Dict = {
  auth: {
    signinTitle: '다시 만나서 반가워요', signinSub: '자리는 그대로 데워 뒀어요. 로그인하고 하던 판을 이어 가세요.',
    signupTitle: '자리에 앉으세요', signupSub: '가입은 1분이면 끝. 바로 카드를 돌릴게요.',
    email: '이메일', password: '비밀번호', name: '이름', namePh: '아오이가 뭐라고 부를까요?',
    newPassword: '새 비밀번호', confirm: '비밀번호 확인',
    pwHint: '10자 이상. 짧은 문장으로 만들면 좋아요.',
    pwTips: ['10자 이상', '대문자와 소문자', '숫자 또는 기호'],
    show: '보기', hide: '숨기기', showPw: '비밀번호 보기', hidePw: '비밀번호 숨기기',
    signin: '로그인', signup: '계정 만들기', forgot: '비밀번호를 잊으셨나요?',
    noAccount: '처음이세요?', haveAccount: '이미 계정이 있나요?',
    agree: '계정을 만들면 {terms} 및 {privacy}에 동의하게 됩니다. 칩은 게임 머니(현금 가치 없음)이며, 에이전트는 AI입니다.',
    termsLink: '이용약관', privacyLink: '개인정보 처리방침',
    forgotTitle: '비밀번호를 잊으셨나요?', forgotSub: '이메일을 입력하면 새 비밀번호를 설정할 수 있는 링크를 보내 드려요.', sendLink: '재설정 링크 보내기',
    forgotSent: '가입된 이메일이라면 재설정 링크가 곧 도착해요. 링크는 1시간 동안 유효합니다.',
    resetTitle: '새 비밀번호 설정', resetSub: '이 기기에서는 로그인되고, 다른 모든 기기에서는 로그아웃돼요.', reset: '새 비밀번호 저장',
    resetDone: '비밀번호가 변경되었어요.', linkInvalid: '유효하지 않거나 만료된 링크예요.',
    verifyTitle: '메일함을 확인해 주세요', verifySub: '확인 링크를 보낸 주소:', verifyHelp: '어느 기기에서 열어도 괜찮아요. 링크를 누르면 이 페이지가 알아서 넘어갑니다.',
    resend: '다시 보내기', resent: '보냈어요. 스팸함도 확인해 주세요.', waiting: '링크를 누르기를 기다리는 중…',
    verifying: '이메일 확인 중…', verified: '입장 완료!', verifiedSub: '이메일이 확인됐어요. 아오이가 자리를 마련해 뒀어요.', confirmed: '이메일 확인 완료', confirmedSub: '이메일이 확인됐어요. 로그인하고 계속하세요.',
    continue: '게임 시작', backToSignin: '로그인으로 돌아가기', mismatch: '비밀번호가 서로 달라요', mailOff: '이 서버에는 아직 이메일이 설정되지 않았어요. 관리자에게 링크를 요청해 주세요.',
    badCreds: '이메일 또는 비밀번호가 올바르지 않아요.', offline: '서버에 연결할 수 없어요. 네트워크를 확인하고 다시 시도해 주세요.',
    strength: ['너무 짧아요', '보통', '좋아요', '강력해요'],
    signout: '로그아웃', useOther: '다른 계정 사용',
    lines: {
      signin: '“설욕전하러 왔구나? 자리 따뜻하게 데워 놨어.”',
      signup: '“새로운 플레이어다! 살살 해 줄게. 딱 한 판만.”',
      verify: '“메일에서 한 번만 클릭하면 바로 카드 돌린다!”',
      forgot: '“누구나 깜빡할 때가 있지. 렌도 가끔은 그래. 아주 가끔.”',
      reset: '“새 비밀번호, 새로운 운. 이번엔 튼튼하게 가자.”',
    },
    tagline: '아오이 葵 · 모든 테이블의 호스트',
    serverErrors: {
      'an account with this email already exists': '이미 가입된 이메일이에요',
      'that email address does not look valid': '올바른 이메일 주소가 아닌 것 같아요',
      'password must be at least 10 characters': '비밀번호는 10자 이상이어야 해요',
      'password must not be your email address': '이메일 주소를 비밀번호로 쓸 수 없어요',
    },
  },
  app: {
    brand: 'Play with Agents',
    chat: '아오이', play: '플레이', studio: '스튜디오', settings: '설정', chats: '대화',
    newChat: '새 대화', search: '대화 검색', archived: '보관됨', showArchived: '보관함 보기', hideArchived: '보관함 숨기기',
    empty: '아직 대화가 없어요. 아오이에게 인사해 보세요.', noMatch: '일치하는 대화가 없어요.',
    rename: '이름 바꾸기', archive: '보관', unarchive: '복원', delete: '삭제',
    confirmDelete: '이 대화를 완전히 삭제할까요? 대화에서 시작한 테이블과 게임은 그대로 남아요.',
    collapse: '사이드바 접기', expand: '사이드바 펼치기', menu: '메뉴', more: '더 보기',
    theme: '테마', dark: '다크', light: '라이트', language: '언어', signout: '로그아웃', profile: '프로필',
    live: '연결됨', offline: '다시 연결하는 중…', untitled: '새 대화',
  },
  chat: {
    hello: '안녕하세요, {name}님! 저는 아오이예요.', helloAnon: '안녕하세요! 저는 아오이예요.',
    intro: '여기 테이블의 호스트예요. 제 친구들과 텍사스 홀덤 한 판 붙여 드릴 수도 있고, 기초부터 차근차근 알려 드릴 수도 있고, 완전히 새로운 게임을 같이 만들 수도 있어요. 오늘은 뭐 하고 놀까요?',
    suggestions: ['미카, 렌이랑 홀덤 한 판 할래요', '2분 만에 포커 배우기', '같이 게임 하나 만들어요', '다른 에이전트들은 누구예요?'],
    placeholder: '아오이에게 메시지 보내기…', send: '보내기', stop: '중지', thinking: '아오이가 생각 중이에요', typing: '아오이가 입력 중이에요',
    hint: 'Enter로 전송 · Shift+Enter로 줄바꿈',
    note: '아오이는 AI예요. 칩은 게임 머니(현금 가치 없음)입니다.',
    failed: '전송하지 못했어요.', retry: '다시 시도', copy: '복사', copied: '복사됨',
    offline: '지금은 아오이와 연결할 수 없어요. 메시지는 안전하게 남아 있으니 잠시 후 다시 시도해 주세요.',
    speak: '아오이 목소리 듣기', stopSpeaking: '중지', enableVoice: '눌러서 아오이 목소리 듣기', speaking: '아오이가 말하는 중',
    role: '당신의 호스트 · AI 에이전트', you: '나', jump: '최신 메시지로', loadError: '이 대화를 불러오지 못했어요.',
  },
  cards: {
    mission: '스튜디오 미션', game: '게임', table: '테이블',
    viewMission: '미션 열기', play: '플레이', publishQ: '공개할까요?', publishHint: '에이전트들이 작업을 마쳤어요. 다른 사람들도 찾을 수 있게 {name}을(를) 공개할까요?',
    publish: '공개하기', notYet: '나중에', published: '공개됨', declined: '초안으로 보관',
    seats: '{min}–{max}인', seatsOne: '{n}인', plays: '{n}회 플레이', loading: '불러오는 중…', unavailable: '지금은 이용할 수 없어요.',
    planning: '제작 계획 세우는 중…', step: '{total}단계 중 {done}단계',
  },
  steps: { design: '규칙 설계', build: '게임 모듈 만들기', cover: '표지 그리기', playtest: '수백 판 플레이테스트', review: '독립 리뷰', publish: '주인에게 공개 여부 묻기', revise: '모듈 수정 ({n}차)', playtestAgain: '다시 플레이테스트 ({n}차)', reviewAgain: '다시 리뷰 ({n}차)' },
  roles: {
    designer: '디자이너', engineer: '엔지니어', playtester: '플레이테스터', artist: '아티스트', critic: '크리틱', coordinator: '아오이',
    doing: {
      designer: '규칙 쓰는 중', engineer: '게임 모듈 만드는 중', playtester: '수백 판 플레이테스트 중', artist: '커버 그리는 중', critic: '규칙과 구현 대조 중', coordinator: '팀 조율 중',
    },
  },
  mission: {
    status: { planning: '계획 중', active: '제작 중', paused: '일시정지', needs_attention: '확인 필요', completed: '완료', failed: '중단됨', cancelled: '취소됨' },
    task: { blocked: '대기(막힘)', ready: '준비됨', leased: '진행 중', running: '진행 중', waiting_approval: '확인 필요', succeeded: '완료', done: '완료', failed: '실패', retrying: '재시도 중', cancelled: '취소됨', skipped: '건너뜀' } as Record<string, string>,
    pause: '일시정지', resume: '재개', cancel: '취소', confirmCancel: '이 제작을 취소할까요? 진행 중인 작업은 멈추지만, 이미 저장된 초안은 그대로 남아요.',
    lanes: '팀', steps: '제작 단계', timeline: '진행 기록', report: '플레이테스트 보고서', critic: '크리틱 의견',
    approvals: '결정해 주세요', next: '다음 할 일', nothingNext: '예정된 작업이 없어요.', earlier: '이전',
    attention: '아오이가 확인을 부탁해요', back: '스튜디오', chat: '아오이와 대화', playDraft: '초안 플레이', more: '전체 보기', less: '접기',
    noReport: '플레이테스터가 아직 보고하지 않았어요.', noCritic: '크리틱이 아직 의견을 내지 않았어요.', noEvents: '아직 진행된 일이 없어요.',
    idle: '대기 중', working: '작업 중', done: '완료', why: '이유', changed: '변경 사항', attempt: '{n}번째 시도',
    usage: '작업량', calls: '회 작업', cost: '예상 비용', notFound: '존재하지 않거나 내 미션이 아니에요.',
    events: {
      'goal.created': '미션 시작', 'goal.planned': '계획 수립', 'goal.replanned': '계획 조정', 'goal.status': '상태 변경',
      'task.started': '시작', 'task.resumed': '체크포인트에서 재개', 'task.succeeded': '완료', 'task.verified': '검증 완료',
      'task.verify_failed': '검사 실패: 수정 중', 'task.retry': '재시도 예정', 'task.failed': '실패', 'task.released': '안전하게 일시정지',
      'task.lease_lost': '인계됨', 'lease.reclaimed': '중단 후 복구됨', 'tool.called': '도구 사용',
      'approval.requested': '승인 요청', 'approval.decided': '결정 완료', 'budget.exceeded': '한도 도달',
    } as Record<string, string>,
  },
  studio: {
    title: '스튜디오', sub: '게임을 설명해 주세요. 아오이의 팀이 디자인하고, 만들고, 플레이테스트한 뒤 테이블에 올려 드려요.',
    heroA: '상상만 하세요. ', heroEm: '만드는 건 저희가.',
    newGame: '새 게임', mine: '내 게임', workshop: '작업실', noGames: '아직 게임이 없어요. 첫 게임은 한 문장이면 시작돼요.',
    noMissions: '지금은 작업대가 비어 있어요.', play: '플레이', revise: '수정하기', visibility: '공개 범위',
    vis: { private: '비공개', unlisted: '링크 공개', public: '전체 공개' } as Record<string, string>,
    status: { building: '제작 중', draft: '초안', published: '공개됨' } as Record<string, string>,
    reviseText: '{name}을(를) 수정해 보자: ', starter: '이런 게임을 만들어 보자: ',
    dialogTitle: '어떤 게임을 만들까요?', dialogSub: '한두 문장이면 충분해요. 테마, 이기는 방법, 좋아하는 요소 무엇이든요.',
    dialogPh: '2~4인용 해적 주사위 게임. 각자 보물을 두고 허세를 부리는…',
    build: '제작 시작', talk: '아오이와 이야기해 보기', building: '시작하는 중…', offline: '스튜디오가 잠시 쉬고 있어요. 조금 뒤 다시 시도하거나, 채팅으로 아오이에게 설명해 주세요.',
    open: '열기', loadError: '스튜디오를 불러오지 못했어요.',
  },
  design: {
    badge: '디자인', title: '아오이와 게임 디자인', role: '함께 게임을 디자인하는 중', start: '아오이와 디자인하기',
    startSub: '새로운 게임 아이디어를 함께 탐색하고, 마음에 들면 만들어요.',
    emptyTitle: '아무도 해 본 적 없는 게임을 만들어 봐요',
    emptyIntro: '테마, 느낌, 좋아하는 메커니즘을 알려 주세요. 방향을 제안하고, 허점을 찾고, 옆에 디자인 노트를 정리할게요. 감이 오면 제작 계획을 써 드릴게요.',
    suggestions: ['깜짝 놀랄 아이디어 세 개', '비 오는 저녁용 아늑한 2인 게임', '고전 비틀기: 체스인데…', '보드가 거짓말하는 블러핑 게임'],
    placeholder: '아이디어를 던지거나 “만약에…”라고 물어보세요', notes: '디자인 노트', notesEmpty: '디자인하는 동안 노트가 여기에 쌓여요.',
    f: { pitch: '한 줄 소개', players: '인원', length: '플레이 시간', board: '보드와 테이블', components: '구성물', loop: '한 턴', mechanics: '핵심 메커니즘', twist: '새로운 점', win: '승리 조건', open_questions: '미정', parked: '보류한 아이디어' },
    readiness: '제작 준비도', makePlan: '제작 계획 만들기', making: '계획 작성 중…', updatePlan: '계획 업데이트',
    stale: '계획을 쓴 뒤에 디자인이 바뀌었어요.', plan: '제작 계획', build: '이 게임 만들기', building: '제작 시작 중…',
    keep: '계속 디자인하기', built: '스튜디오가 만드는 중', openMission: '진행 상황 보기', showNotes: '노트', hideNotes: '노트 숨기기',
    planFailed: '계획을 쓰지 못했어요: {e}', buildFailed: '제작을 시작하지 못했어요: {e}', notReady: '조금만 더 디자인해 볼까요? 규칙이 대부분 정해지면 계획이 더 탄탄해져요.',
  },
  settings: {
    title: '설정', saved: '저장됨', saveFailed: '저장하지 못했어요: {e}', save: '저장',
    groups: { profile: '프로필', aoi: '아오이', table: '테이블', agents: '에이전트', studio: '스튜디오', notifications: '알림', appearance: '화면', privacy: '개인정보 및 기억', security: '보안', usage: '사용량 및 한도' } as Record<string, string>,
    hints: {
      profile: '테이블에서 보이는 내 모습.', aoi: '아오이가 말을 거는 방식.', table: '테이블의 모습, 소리, 움직임.',
      agents: '누가 함께 앉고, 얼마나 세게 나오는지.', studio: '내가 만드는 게임의 기본값.',
      notifications: '이메일로 받을 소식.', appearance: '테마와 글자 크기.', privacy: '아오이가 기억하는 것과 내 데이터.',
      security: '비밀번호와 로그인된 기기.', usage: '지금까지 사용한 양.',
    } as Record<string, string>,
    displayName: '표시 이름', displayHint: '같은 테이블의 플레이어에게 보여요.', email: '이메일', joined: '가입일', language: '언어',
    languageHint: '아오이의 답변과 이메일이 이 언어로 와요.',
    aoiTone: '말투', tones: { playful: '장난스럽게', calm: '차분하게', competitive: '승부욕 있게' },
    previews: { playful: '“오, 레이즈? 대담한데. 그런 거 좋아.”', calm: '“천천히 해. 팟은 어디 안 가.”', competitive: '“콜. 뭘 들고 있는지 보여 줘.”' },
    aoiTalk: '말의 양', talks: { chatty: '수다스럽게', normal: '보통', quiet: '조용히' },
    coaching: '내 차례에 코칭 받기', coachingHint: '내 차례가 되면 아오이가 살짝 팁을 알려 줘요. 나만 볼 수 있어요.',
    callMe: '호칭', callMeHint: '아오이가 부를 별명.', callMePh: '예: 선장님',
    voice: '아오이 목소리', voiceOn: '아오이 음성 켜기', voiceHint: '아오이의 메시지에 스피커 버튼이 생겨요.', autoplay: '답변 자동으로 읽어 주기', autoplayHint: '새 답변만, 답변이 끝난 뒤에 읽어요.',
    tableVoice: '테이블에서도 말하기', tableVoiceHint: '테이블 토크를 소리로 들려줘요.', volume: '음량', hear: '아오이 목소리 듣기',
    sample: '안녕하세요, 아오이예요! 어서 앉아요, 곧 카드를 돌릴 거예요. 잘 부탁해요!',
    memory: '아오이가 나에 대해 기억하도록 허용', memoryHint: '좋아하는 게임, 플레이 스타일, 불리고 싶은 별명.',
    turnClock: '턴 타이머', turnHint: '내가 연 테이블에서 한 수당 주어지는 시간.', secs: '{n}초', noClock: '없음',
    noClockHint: '타이머 없음은 다른 사람 플레이어가 없는 테이블에서만 적용돼요.',
    agentSpeed: '에이전트 속도', speeds: { fast: '빠르게', natural: '자연스럽게', slow: '느리게' },
    tableTalk: '테이블 토크', talkHint: '에이전트가 스스로 말을 거는 빈도예요. 끄기로 해도 이름을 부르거나 @, 답장으로 말을 걸면 대답해요.', talkModes: { all: '전부', quiet: '조금만', off: '끄기' },
    fourColor: '4색 덱', fourColorHint: '다이아는 파란색, 클럽은 초록색.', handStrength: '내 핸드 강도 표시',
    sound: '사운드', motion: '모션', motions: { full: '전체', reduced: '줄이기' },
    cardBack: '카드 뒷면', backs: { aoi: '아오이', classic: '클래식', midnight: '미드나잇' },
    felt: '테이블 펠트', felts: { navy: '네이비', emerald: '에메랄드', crimson: '크림슨' },
    difficulty: '에이전트 난이도', difficulties: { casual: '캐주얼', regular: '보통', shark: '샤크' },
    fill: '빈자리에 에이전트 앉히기', fillHint: '친구가 안 오면 에이전트가 대신 앉아요.',
    favourites: '좋아하는 에이전트', favouritesHint: '아오이가 이 에이전트들부터 초대해요.',
    studioVis: '새 게임 기본 공개 범위', playtests: '제작당 플레이테스트 판 수', playtestHint: '판 수가 많을수록 밸런스는 정교해지고, 제작은 느려져요.',
    emailInvites: '누군가 나를 테이블에 초대했을 때', emailTurn: '내 차례인데 자리를 비웠을 때', emailBuild: '요청한 게임이 완성됐을 때',
    theme: '테마', themes: { dark: '다크', light: '라이트', system: '시스템 설정' }, fontSize: '글자 크기', sizes: { small: '작게', medium: '보통', large: '크게' },
    memories: '아오이가 기억하는 것', forget: '잊기', forgetAll: '전부 잊기', noMemories: '아직 없어요.', confirmForgetAll: '아오이가 기억하는 나에 대한 모든 것을 잊게 할까요?',
    export: '내 데이터 내보내기', exportHint: '계정, 게임, 대화, 미션 기록을 JSON 파일 하나로.', policy: '개인정보 처리방침',
    deleteAccount: '계정 삭제', deleteHint: '계정, 대화, 테이블, 게임이 영구 삭제되며 되돌릴 수 없어요.', deleteConfirm: '비밀번호',
    password: '비밀번호 변경', current: '현재 비밀번호', next: '새 비밀번호', updatePw: '비밀번호 변경',
    sessions: '로그인된 기기', thisDevice: '이 기기', revoke: '로그아웃', revokeOthers: '다른 모든 기기에서 로그아웃', lastSeen: '최근 접속',
    today: '오늘', month: '이번 달', tokens: '토큰', usageHint: '아오이와 스튜디오 에이전트는 언어 모델로 작동해요. 지금까지 사용한 양이에요.',
    missions: '최근 미션', maxCost: '제작당 최대 예상 비용(USD)', maxDays: '제작당 최대 일수', limitsHint: '제작이 한도에 도달하면 일시정지되고, 아오이가 먼저 물어봐요.',
    offline: '지금은 설정을 불러올 수 없어요.',
  },
  legal: { home: '홈으로', contents: '목차', note: '게임 머니 전용(현금 가치 없음). 결제도, 도박도 없습니다. 에이전트는 AI입니다.' },
  common: {
    loading: '불러오는 중…', error: '문제가 생겼어요', retry: '다시 시도', close: '닫기', back: '뒤로', cancel: '취소', confirm: '확인', yes: '예', no: '아니요', open: '열기', language: '언어', offline: '오프라인', save: '저장', done: '완료',
    ago: { now: '방금', m: '{n}분 전', h: '{n}시간 전', d: '{n}일 전' },
  },
}

const ja: Dict = {
  auth: {
    signinTitle: 'おかえりなさい', signinSub: '席はあたためておきました。ログインして、続きから遊びましょう。',
    signupTitle: 'さあ、席へどうぞ', signupSub: '登録は1分で完了。すぐにカードを配ります。',
    email: 'メールアドレス', password: 'パスワード', name: 'お名前', namePh: '葵になんて呼ばれたい？',
    newPassword: '新しいパスワード', confirm: 'パスワード（確認）',
    pwHint: '10文字以上。短い文にすると覚えやすいですよ。',
    pwTips: ['10文字以上', '大文字と小文字', '数字または記号'],
    show: '表示', hide: '隠す', showPw: 'パスワードを表示', hidePw: 'パスワードを隠す',
    signin: 'ログイン', signup: 'アカウントを作成', forgot: 'パスワードをお忘れですか？',
    noAccount: 'はじめての方', haveAccount: 'アカウントをお持ちの方',
    agree: 'アカウントを作成すると、{terms}と{privacy}に同意したことになります。チップはプレイマネー（現金価値なし）で、エージェントは AI です。',
    termsLink: '利用規約', privacyLink: 'プライバシーポリシー',
    forgotTitle: 'パスワードをお忘れですか？', forgotSub: 'メールアドレスを入力すると、新しいパスワードを設定するリンクをお送りします。', sendLink: '再設定リンクを送信',
    forgotSent: 'このアドレスのアカウントがあれば、再設定リンクをお送りしました。リンクの有効期限は1時間です。',
    resetTitle: '新しいパスワードを設定', resetSub: 'この端末ではログインしたまま、ほかのすべての端末からはログアウトします。', reset: '新しいパスワードを保存',
    resetDone: 'パスワードを更新しました。', linkInvalid: 'このリンクは無効か、有効期限が切れています。',
    verifyTitle: 'メールを確認してください', verifySub: '確認リンクの送信先：', verifyHelp: 'どの端末で開いても大丈夫。リンクを開くと、このページは自動で進みます。',
    resend: 'もう一度送る', resent: '送信しました。迷惑メールフォルダもご確認ください。', waiting: 'リンクのクリックを待っています…',
    verifying: 'メールアドレスを確認しています…', verified: 'ようこそ！', verifiedSub: 'メールアドレスを確認しました。葵が席を用意して待っています。', confirmed: '確認完了', confirmedSub: 'メールアドレスを確認しました。ログインして続けてください。',
    continue: 'さあ、遊ぼう', backToSignin: 'ログインに戻る', mismatch: 'パスワードが一致しません', mailOff: 'このサーバーではまだメールが設定されていません。管理者にリンクをお尋ねください。',
    badCreds: 'メールアドレスまたはパスワードが正しくありません。', offline: 'サーバーに接続できません。通信環境を確認して、もう一度お試しください。',
    strength: ['短すぎます', 'まあまあ', '良い', '強い'],
    signout: 'ログアウト', useOther: '別のアカウントを使う',
    lines: {
      signin: '「リベンジしに来た？席、あっためておいたよ。」',
      signup: '「新しいプレイヤーだ！手加減してあげる。最初の1ハンドだけね。」',
      verify: '「メールのリンクをポチッとしたら、すぐ配るよ。」',
      forgot: '「誰にでもあることだよ。レンだって忘れることあるし。たまーにね。」',
      reset: '「新しいパスワードで、運も一新。強いのにしよう！」',
    },
    tagline: '葵 Aoi · すべてのテーブルのホスト',
    serverErrors: {
      'an account with this email already exists': 'このメールアドレスはすでに登録されています',
      'that email address does not look valid': 'メールアドレスの形式が正しくないようです',
      'password must be at least 10 characters': 'パスワードは10文字以上にしてください',
      'password must not be your email address': 'メールアドレスをパスワードにすることはできません',
    },
  },
  app: {
    brand: 'Play with Agents',
    chat: '葵', play: 'プレイ', studio: 'スタジオ', settings: '設定', chats: 'チャット',
    newChat: '新しいチャット', search: 'チャットを検索', archived: 'アーカイブ', showArchived: 'アーカイブを表示', hideArchived: 'アーカイブを隠す',
    empty: 'まだチャットはありません。葵にあいさつしてみましょう。', noMatch: '一致するチャットはありません。',
    rename: '名前を変更', archive: 'アーカイブ', unarchive: '元に戻す', delete: '削除',
    confirmDelete: 'このチャットを完全に削除しますか？ここから始めたテーブルやゲームはそのまま残ります。',
    collapse: 'サイドバーを閉じる', expand: 'サイドバーを開く', menu: 'メニュー', more: 'その他',
    theme: 'テーマ', dark: 'ダーク', light: 'ライト', language: '言語', signout: 'ログアウト', profile: 'プロフィール',
    live: '接続中', offline: '再接続しています…', untitled: '新しいチャット',
  },
  chat: {
    hello: '{name}さん、こんにちは！葵です。', helloAnon: 'こんにちは！葵です。',
    intro: 'ここのテーブルのホストをしています。仲間たちとのテキサスホールデムにご案内したり、基本からルールを教えたり、まったく新しいゲームを一緒に作ったりもできますよ。今日は何して遊ぶ？',
    suggestions: ['ミカとレンとホールデムがしたい', '2分でポーカーを教えて', '一緒にゲームを作ろう', 'ほかのエージェントってどんな人？'],
    placeholder: '葵にメッセージを送る…', send: '送信', stop: '停止', thinking: '葵が考えています', typing: '葵が入力しています',
    hint: 'Enter で送信 · Shift+Enter で改行',
    note: '葵は AI です。チップはプレイマネー（現金価値なし）です。',
    failed: '送信できませんでした。', retry: '再試行', copy: 'コピー', copied: 'コピーしました',
    offline: 'いまは葵につながりません。メッセージは消えていないので、少し待ってからもう一度お試しください。',
    speak: '葵の声を再生', stopSpeaking: '停止', enableVoice: 'タップして葵の声を聞く', speaking: '葵が話しています',
    role: 'あなたのホスト · AI エージェント', you: 'あなた', jump: '最新へ移動', loadError: 'このチャットを読み込めませんでした。',
  },
  cards: {
    mission: 'スタジオのミッション', game: 'ゲーム', table: 'テーブル',
    viewMission: 'ミッションを開く', play: 'プレイ', publishQ: '公開しますか？', publishHint: 'エージェントたちの作業が終わりました。{name} を公開して、みんなが見つけられるようにしますか？',
    publish: '公開する', notYet: 'まだしない', published: '公開済み', declined: '下書きとして保存',
    seats: '{min}〜{max}人', seatsOne: '{n}人', plays: '{n}回プレイ', loading: '読み込み中…', unavailable: 'いまは利用できません。',
    planning: '制作プランを立てています…', step: '{done} / {total} ステップ',
  },
  steps: { design: 'ルールを設計', build: 'ゲームモジュールを作成', cover: 'カバーを描く', playtest: '数百局のテストプレイ', review: '独立レビュー', publish: 'オーナーに公開を確認', revise: 'モジュールを修正（第{n}ラウンド）', playtestAgain: '再テストプレイ（第{n}ラウンド）', reviewAgain: '再レビュー（第{n}ラウンド）' },
  roles: {
    designer: 'デザイナー', engineer: 'エンジニア', playtester: 'テストプレイヤー', artist: 'アーティスト', critic: 'レビュアー', coordinator: '葵',
    doing: {
      designer: 'ルールを執筆中', engineer: 'ゲームモジュールを制作中', playtester: '数百ゲームをテストプレイ中', artist: 'カバーを描いています', critic: 'ルールと実装を照合中', coordinator: 'チームをまとめています',
    },
  },
  mission: {
    status: { planning: '計画中', active: '制作中', paused: '一時停止', needs_attention: '確認待ち', completed: '完了', failed: '停止', cancelled: 'キャンセル' },
    task: { blocked: 'ブロック中', ready: '準備完了', leased: '実行中', running: '実行中', waiting_approval: '確認待ち', succeeded: '完了', done: '完了', failed: '失敗', retrying: '再試行中', cancelled: 'キャンセル', skipped: 'スキップ' } as Record<string, string>,
    pause: '一時停止', resume: '再開', cancel: 'キャンセル', confirmCancel: 'この制作をキャンセルしますか？途中の作業は止まりますが、保存済みの下書きはそのまま残ります。',
    lanes: 'チーム', steps: '制作ステップ', timeline: 'これまでの流れ', report: 'テストプレイレポート', critic: 'レビュー結果',
    approvals: 'あなたの判断', next: '次にやること', nothingNext: '予定はありません。', earlier: 'それ以前',
    attention: '葵があなたを待っています', back: 'スタジオ', chat: '葵とチャット', playDraft: '下書きをプレイ', more: 'すべて表示', less: '閉じる',
    noReport: 'テストプレイヤーからのレポートはまだありません。', noCritic: 'レビュアーの意見はまだありません。', noEvents: 'まだ何も起きていません。',
    idle: '待機中', working: '作業中', done: '完了', why: '理由', changed: '変更点', attempt: '{n}回目の試行',
    usage: '作業量', calls: '回のアクション', cost: '推定コスト', notFound: 'このミッションは存在しないか、あなたのものではありません。',
    events: {
      'goal.created': 'ミッション開始', 'goal.planned': 'プラン作成', 'goal.replanned': 'プランを調整', 'goal.status': 'ステータス変更',
      'task.started': '開始', 'task.resumed': 'チェックポイントから再開', 'task.succeeded': '完了', 'task.verified': 'チェック済み',
      'task.verify_failed': 'チェックで問題発見：修正中', 'task.retry': '再試行予定', 'task.failed': '失敗', 'task.released': '安全に一時停止',
      'task.lease_lost': '引き継ぎ', 'lease.reclaimed': '中断から復旧', 'tool.called': 'ツールを使用',
      'approval.requested': '承認をリクエスト', 'approval.decided': 'あなたが決定', 'budget.exceeded': '上限に到達',
    } as Record<string, string>,
  },
  studio: {
    title: 'スタジオ', sub: 'ゲームのアイデアを話してください。葵のチームがデザインし、作り、テストプレイして、テーブルに並べます。',
    heroA: '思い描くだけ。', heroEm: '作るのはおまかせ。',
    newGame: '新しいゲーム', mine: 'マイゲーム', workshop: '制作中', noGames: 'まだゲームはありません。最初の一本は、ひとことから。',
    noMissions: 'いま作業台には何もありません。', play: 'プレイ', revise: '手直し', visibility: '公開範囲',
    vis: { private: '非公開', unlisted: '限定公開', public: '公開' } as Record<string, string>,
    status: { building: '制作中', draft: '下書き', published: '公開済み' } as Record<string, string>,
    reviseText: '{name} を手直ししよう：', starter: 'こんなゲームを作ろう：',
    dialogTitle: 'どんなゲームを作る？', dialogSub: '一、二文で十分です。テーマ、勝ち方、好きな要素など何でも。',
    dialogPh: '2〜4人で遊ぶ海賊のダイスゲーム。自分のお宝についてハッタリをかけ合う…',
    build: '制作をスタート', talk: '葵に相談する', building: '開始しています…', offline: 'スタジオはいまオフラインです。少し待ってから試すか、チャットで葵に話してみてください。',
    open: '開く', loadError: 'スタジオを読み込めませんでした。',
  },
  design: {
    badge: 'デザイン', title: '葵とゲームデザイン', role: '一緒にゲームをデザイン中', start: '葵とデザインする',
    startSub: '新しい遊びを一緒に探して、気に入ったら作ろう。',
    emptyTitle: 'まだ誰も遊んだことのないゲームを発明しよう',
    emptyIntro: 'テーマや雰囲気、好きなメカニクスを教えて。方向性をいくつか出して、穴を探して、横にデザインノートをまとめていくね。ピンときたら制作プランを書くよ。',
    suggestions: ['びっくりするアイデアを3つ', '雨の夜にぴったりの2人用ゲーム', '名作をひねる：チェスだけど…', '盤面がウソをつくブラフゲーム'],
    placeholder: 'アイデアを話すか、「もし…なら？」と聞いてみて', notes: 'デザインノート', notesEmpty: 'デザインを進めると、ここにノートがたまっていくよ。',
    f: { pitch: 'ひとこと', players: '人数', length: 'プレイ時間', board: 'ボードとテーブル', components: 'コンポーネント', loop: '1ターン', mechanics: 'メカニクス', twist: '新しさ', win: '勝利条件', open_questions: '未決定', parked: '保留のアイデア' },
    readiness: '制作準備度', makePlan: '制作プランを作る', making: 'プランを書いています…', updatePlan: 'プランを更新',
    stale: 'プランを書いたあとでデザインが変わりました。', plan: '制作プラン', build: 'このゲームを作る', building: '制作を開始中…',
    keep: 'デザインを続ける', built: 'スタジオが制作中', openMission: '進行状況を見る', showNotes: 'ノート', hideNotes: 'ノートを閉じる',
    planFailed: 'プランを書けませんでした：{e}', buildFailed: '制作を開始できませんでした：{e}', notReady: 'もう少しデザインしてみる？ルールがだいたい決まってからの方が、いいプランになるよ。',
  },
  settings: {
    title: '設定', saved: '保存しました', saveFailed: '保存できませんでした：{e}', save: '保存',
    groups: { profile: 'プロフィール', aoi: '葵', table: 'テーブル', agents: 'エージェント', studio: 'スタジオ', notifications: '通知', appearance: '表示', privacy: 'プライバシーと記憶', security: 'セキュリティ', usage: '使用量と上限' } as Record<string, string>,
    hints: {
      profile: 'テーブルでのあなたの見え方。', aoi: '葵の話し方。', table: 'テーブルの見た目、音、動き。',
      agents: '誰と同じ卓に座り、どれだけ本気で来るか。', studio: '作るゲームの初期設定。',
      notifications: 'メールでお知らせする内容。', appearance: 'テーマと文字サイズ。', privacy: '葵が覚えていることと、あなたのデータ。',
      security: 'パスワードとログイン中の端末。', usage: 'これまでの使用量。',
    } as Record<string, string>,
    displayName: '表示名', displayHint: '同じテーブルのプレイヤーに表示されます。', email: 'メールアドレス', joined: '登録日', language: '言語',
    languageHint: '葵の返事やメールがこの言語になります。',
    aoiTone: '話し方', tones: { playful: 'おちゃめ', calm: 'おだやか', competitive: '負けず嫌い' },
    previews: { playful: '「お、レイズ？大胆だね。そういうの好き。」', calm: '「ゆっくりでいいよ。ポットは逃げないから。」', competitive: '「コール。さあ、手の内を見せて。」' },
    aoiTalk: 'おしゃべり度', talks: { chatty: 'おしゃべり', normal: 'ふつう', quiet: '控えめ' },
    coaching: 'プレイ中にアドバイスをもらう', coachingHint: 'あなたの番になると、葵がこっそりヒントをくれます。あなたにだけ見えます。',
    callMe: '呼び名', callMeHint: '葵があなたを呼ぶときのニックネーム。', callMePh: '例：キャプテン',
    voice: '葵の声', voiceOn: '葵の声をオンにする', voiceHint: 'メッセージにスピーカーボタンが付きます。', autoplay: '返事を自動で読み上げる', autoplayHint: '新しい返事だけを、書き終わってから読み上げます。',
    tableVoice: 'テーブルでも話す', tableVoiceHint: 'テーブルトークを声で聞けます。', volume: '音量', hear: '葵の声を聞く',
    sample: 'こんにちは、葵です！さあ座って、もうすぐカードを配るよ。よろしくね！',
    memory: '葵に自分のことを覚えてもらう', memoryHint: '好きなゲーム、プレイスタイル、呼ばれたいニックネーム。',
    turnClock: '持ち時間', turnHint: 'あなたが開いたテーブルでの1手ごとの時間。', secs: '{n}秒', noClock: 'なし',
    noClockHint: '持ち時間なしは、ほかに人間のプレイヤーがいないテーブルでのみ有効です。',
    agentSpeed: 'エージェントの速さ', speeds: { fast: '速い', natural: '自然', slow: 'ゆっくり' },
    tableTalk: 'テーブルトーク', talkHint: 'エージェントが自分から話す頻度。オフでも、名前や@、返信で話しかければ答えます。', talkModes: { all: 'すべて', quiet: '控えめ', off: 'オフ' },
    fourColor: '4色デッキ', fourColorHint: 'ダイヤは青、クラブは緑。', handStrength: '自分の役の強さを表示',
    sound: 'サウンド', motion: 'アニメーション', motions: { full: 'フル', reduced: '控えめ' },
    cardBack: 'カードの裏面', backs: { aoi: '葵', classic: 'クラシック', midnight: 'ミッドナイト' },
    felt: 'テーブルのラシャ', felts: { navy: 'ネイビー', emerald: 'エメラルド', crimson: 'クリムゾン' },
    difficulty: 'エージェントの強さ', difficulties: { casual: 'カジュアル', regular: 'ふつう', shark: 'シャーク' },
    fill: '空席をエージェントで埋める', fillHint: '友だちが来ないときは、エージェントが代わりに座ります。',
    favourites: 'お気に入りのエージェント', favouritesHint: '葵はこのエージェントから先に誘います。',
    studioVis: '新しいゲームの公開範囲', playtests: '1回の制作あたりのテストプレイ数', playtestHint: '回数が多いほどバランスは良くなり、制作には時間がかかります。',
    emailInvites: 'テーブルに招待されたとき', emailTurn: '自分の番なのに席を外しているとき', emailBuild: '頼んだゲームが完成したとき',
    theme: 'テーマ', themes: { dark: 'ダーク', light: 'ライト', system: 'システム設定' }, fontSize: '文字サイズ', sizes: { small: '小', medium: '中', large: '大' },
    memories: '葵が覚えていること', forget: '忘れる', forgetAll: 'すべて忘れる', noMemories: 'まだありません。', confirmForgetAll: '葵があなたについて覚えていることを、すべて忘れさせますか？',
    export: 'データをエクスポート', exportHint: 'アカウント、ゲーム、チャット、ミッション履歴を一つの JSON ファイルに。', policy: 'プライバシーポリシー',
    deleteAccount: 'アカウントを削除', deleteHint: 'アカウント、チャット、テーブル、ゲームが完全に削除されます。元に戻すことはできません。', deleteConfirm: 'パスワード',
    password: 'パスワードを変更', current: '現在のパスワード', next: '新しいパスワード', updatePw: 'パスワードを更新',
    sessions: 'ログイン中の端末', thisDevice: 'この端末', revoke: 'ログアウト', revokeOthers: 'ほかのすべての端末からログアウト', lastSeen: '最終アクセス',
    today: '今日', month: '今月', tokens: 'トークン', usageHint: '葵とスタジオのエージェントは言語モデルで動いています。これまでの使用量です。',
    missions: '最近のミッション', maxCost: '1回の制作あたりの推定コスト上限（USD）', maxDays: '1回の制作あたりの最大日数', limitsHint: '制作が上限に達すると一時停止し、まず葵があなたに確認します。',
    offline: 'いまは設定を読み込めません。',
  },
  legal: { home: 'ホームへ戻る', contents: '目次', note: 'プレイマネーのみ（現金価値なし）。課金なし、ギャンブルなし。エージェントは AI です。' },
  common: {
    loading: '読み込み中…', error: '問題が発生しました', retry: 'もう一度', close: '閉じる', back: '戻る', cancel: 'キャンセル', confirm: '確認', yes: 'はい', no: 'いいえ', open: '開く', language: '言語', offline: 'オフライン', save: '保存', done: '完了',
    ago: { now: 'たった今', m: '{n}分前', h: '{n}時間前', d: '{n}日前' },
  },
}

// Deep-merge a translation over English so a gap (a key added to `en` while a
// translation lags, or a cast Record missing an entry) shows English, never
// the key or `undefined`.
function withFallback<T>(base: T, over: unknown): T {
  if (over === undefined || over === null) return base
  if (Array.isArray(base)) {
    if (!Array.isArray(over)) return base
    return base.map((b, i) => (i < over.length ? withFallback(b, over[i]) : b)).concat(over.slice(base.length)) as T
  }
  if (base && typeof base === 'object') {
    if (typeof over !== 'object') return base
    const out: Record<string, unknown> = { ...(over as Record<string, unknown>) }
    for (const [k, v] of Object.entries(base as Record<string, unknown>)) out[k] = withFallback(v, (over as Record<string, unknown>)[k])
    return out as T
  }
  return (typeof over === typeof base ? over : base) as T
}

const dicts: Record<Lang, Dict> = { en, zh: withFallback(en, zh), ko: withFallback(en, ko), ja: withFallback(en, ja) }

// For code outside React (formatters, model helpers).
export const dictFor = (lang: string): Dict => dicts[isLang(lang) ? lang : 'en']
export const fmt = (s: string, vars: Record<string, string | number>) => s.replace(/\{(\w+)\}/g, (_, k) => String(vars[k] ?? ''))

// The UI's current locale, for code that has no hook at hand (<html lang> is
// kept in step with the provider).
export const uiLocale = () => (typeof document !== 'undefined' && document.documentElement.lang) || 'en'

// Locale-aware formatting, usable with or without the hook.
export const fmtNumber = (n: number, lang: string, opts?: Intl.NumberFormatOptions) => new Intl.NumberFormat(localeOf(lang), opts).format(n || 0)
export function fmtDate(d: string | number | Date, lang: string, opts?: Intl.DateTimeFormatOptions) {
  const x = d instanceof Date ? d : new Date(d)
  return isNaN(x.getTime()) ? '' : x.toLocaleString(localeOf(lang), opts)
}

// The agents' names and blurbs in Korean and Japanese. The API carries en and
// zh (name_zh, …); the server's `i18n[lang]` block (or a `name_ko` field) wins over these.
type AgentText = { name: string; title: string; bio: string }
const AGENT_TEXT: Partial<Record<Lang, Record<string, AgentText>>> = {
  ko: {
    aoi: { name: '아오이', title: '호스트', bio: '다정하고 장난기 많고 승부욕 강한 호스트. 균형 잡힌 플레이에, 상대에 맞춰 스타일을 바꿔요.' },
    ren: { name: '렌', title: '전략가', bio: '침착하고 말수가 적으며, 유머는 건조한 편. 타이트 어그레시브.' },
    mika: { name: '미카', title: '무대 체질', bio: '겁 없고 시끌벅적. 올인이라면 사족을 못 써요.' },
    bram: { name: '브램 선장', title: '노련한 뱃사람', bio: '일단 콜부터. 허풍 섞인 무용담은 절대 폴드하지 않죠.' },
    nova: { name: '노바', title: '로봇', bio: '명랑하고 확률을 줄줄 읊어요. 농담 실력은 처참.' },
    lin: { name: '린', title: '천재 소녀', bio: '수줍고 예의 바르지만, 조용히 치명적이에요.' },
  },
  ja: {
    aoi: { name: '葵', title: 'ホスト', bio: 'あたたかくて、ちょっぴりイジワルで、負けず嫌い。バランス型で、相手に合わせて戦い方を変える。' },
    ren: { name: 'レン', title: '策士', bio: '冷静で口数が少なく、ユーモアはドライ。タイト・アグレッシブ。' },
    mika: { name: 'ミカ', title: '目立ちたがり', bio: '怖いもの知らずでにぎやか。オールインが大好き。' },
    bram: { name: 'ブラム船長', title: 'ベテラン船乗り', bio: 'とにかくコールしがち。ほら話だけは絶対にフォールドしない。' },
    nova: { name: 'ノヴァ', title: 'ロボット', bio: '陽気で、確率をすらすら語る。ジョークは壊滅的。' },
    lin: { name: 'リン', title: '天才少女', bio: '内気で礼儀正しいのに、静かに容赦ない。' },
  },
}

type AgentLike = { id: string; name: string; [k: string]: unknown }
// agentText(a, 'name' | 'title' | 'bio', lang): the agent's text in the UI language.
export function agentText(a: AgentLike | undefined | null, field: keyof AgentText, lang: string): string {
  if (!a) return ''
  const own = (k: string) => (typeof a[k] === 'string' && a[k] ? (a[k] as string) : '')
  // The API's per-language block wins: i18n: { en|zh|ko|ja: { name, title, bio } }.
  const i18n = a.i18n as Partial<Record<string, Partial<AgentText>>> | undefined
  const api = i18n?.[lang]?.[field]
  if (api) return api
  if (lang !== 'en') {
    const server = own(`${field}_${lang}`)
    if (server) return server
    const local = AGENT_TEXT[lang as Lang]?.[a.id]?.[field]
    if (local) return local
  }
  return own(field)
}

type Ctx = {
  lang: Lang
  setLang: (l: Lang) => void
  // The account's language: used only when this device has no saved choice.
  adoptLang: (l: unknown) => void
  t: Dict
  f: (s: string, vars: Record<string, string | number>) => string
  locale: string
  num: (n: number, opts?: Intl.NumberFormatOptions) => string
  date: (d: string | number | Date, opts?: Intl.DateTimeFormatOptions) => string
}
const I18n = createContext<Ctx>(null as unknown as Ctx)

const STORE = 'play.lang'

// The browser's preferred languages, best first, mapped to ours.
export function browserLang(): Lang {
  const list = (typeof navigator !== 'undefined' && (navigator.languages?.length ? navigator.languages : [navigator.language])) || []
  for (const raw of list) {
    const l = (raw || '').toLowerCase()
    if (l.startsWith('ko')) return 'ko'
    if (l.startsWith('ja')) return 'ja'
    if (l.startsWith('zh')) return 'zh'
    if (l.startsWith('en')) return 'en'
  }
  return 'en'
}

function savedLang(): Lang | null {
  try {
    const s = localStorage.getItem(STORE)
    return isLang(s) ? s : null
  } catch {
    return null
  }
}

export function I18nProvider({ children }: { children: ReactNode }) {
  // Order: the choice saved on this device → the account's language (adoptLang,
  // once the session loads) → the browser's languages.
  const [lang, setLangState] = useState<Lang>(() => savedLang() ?? browserLang())
  const setLang = (l: Lang) => {
    setLangState(l)
    try {
      localStorage.setItem(STORE, l)
    } catch {}
  }
  useEffect(() => {
    document.documentElement.lang = LOCALES[lang]
  }, [lang])
  const value = useMemo<Ctx>(() => {
    const locale = LOCALES[lang]
    return {
      lang, setLang, t: dicts[lang], f: fmt, locale,
      adoptLang: (l) => { if (isLang(l) && !savedLang()) setLangState(l) },
      num: (n, opts) => fmtNumber(n, lang, opts),
      date: (d, opts) => fmtDate(d, lang, opts),
    }
  }, [lang])
  return <I18n.Provider value={value}>{children}</I18n.Provider>
}

export const useI18n = () => useContext(I18n)

