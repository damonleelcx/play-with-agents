// Terms of Service and Privacy Policy, in English and Chinese.
//
// These describe what the product actually does. Every data flow named here
// exists in the code; change one, change the other. The operating entity,
// governing law and dispute terms are for the operator's counsel to add and
// are deliberately not guessed here.

export type Section = { h: string; p: string[] }
export type Doc = { title: string; updated: string; intro: string; sections: Section[] }

const UPDATED_EN = 'Last updated 2 October 2026'
const UPDATED_ZH = '最后更新：2026 年 10 月 2 日'
const CONTACT = 'support@heros-agent.space'

export const terms: Record<'en' | 'zh', Doc> = {
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
}

export const privacy: Record<'en' | 'zh', Doc> = {
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
}
