// Terms of Service and Privacy Policy, in English and Chinese.
//
// These describe what the system ACTUALLY does — every data flow named here
// exists in the code, and nothing the code does is left out. Change one, change
// the other. The operating entity, governing law and dispute terms are for the
// operator's counsel to add; they are deliberately not guessed here.

export type Section = { h: string; p: string[] }
export type Doc = { title: string; updated: string; intro: string; sections: Section[] }

const UPDATED_EN = 'Last updated 23 September 2026'
const UPDATED_ZH = '最后更新：2026 年 9 月 23 日'
const CONTACT = 'support@heros-agent.space'

export const terms: Record<'en' | 'zh', Doc> = {
  en: {
    title: 'Terms of Service',
    updated: UPDATED_EN,
    intro: 'These terms cover your use of ACT, including Vera, the AI assistant. Please read them — especially sections 1 and 2.',
    sections: [
      { h: '1. What ACT is — and is not', p: [
        'Vera is an artificial-intelligence assistant. She is not a lawyer or a doctor, and nothing she writes is legal or medical advice for your situation until a licensed professional has reviewed it.',
        'Using ACT does not by itself create an attorney–client or doctor–patient relationship. That relationship exists only when a licensed attorney or physician expressly agrees to represent or treat you.',
        'Actions that require a licence — appearing in court, filing with a court, ordering tests, prescribing — are only ever taken by, or with the approval of, a licensed professional. Some of those actions depend on outside services that may not yet be connected; when an action was not actually carried out, ACT says so.',
      ] },
      { h: '2. Emergencies', p: [
        'ACT is not an emergency service. If you or someone else may be in danger, call 911 (or 120 in mainland China, or your local emergency number) immediately. In the US you can call or text 988 for the Suicide & Crisis Lifeline.',
      ] },
      { h: '3. Who can use ACT', p: [
        'You must be at least 18 years old and able to form a binding contract. You are responsible for the accuracy of what you tell Vera; her work is only as good as the facts she is given.',
      ] },
      { h: '4. Your account', p: [
        'Keep your password private and tell us at ' + CONTACT + ' if you think your account has been accessed without permission. You can see and sign out your devices in Settings → Security.',
      ] },
      { h: '5. Approvals are yours', p: [
        'Letters, emails and requests are sent only after the right person approves the exact content. When you approve something, you are responsible for having read it. Once a message has been sent or a document filed, it cannot be recalled.',
      ] },
      { h: '6. Acceptable use', p: [
        'Do not use ACT to break the law; to deceive a court, fabricate or destroy evidence, or coach false testimony; to obtain controlled substances; to harass anyone; or to interfere with the service (including automated or excessive use). Vera will decline such requests, and we may suspend accounts that attempt them.',
      ] },
      { h: '7. Your content', p: [
        'You keep ownership of what you upload and of the work produced for you. You give ACT permission to store and process it only as needed to provide the service to you, as described in the Privacy Policy. You can export or delete it at any time.',
      ] },
      { h: '8. Limits of AI', p: [
        'AI can be wrong, incomplete or out of date. ACT verifies case citations against public court records and checks medications against public drug labels, but no check is perfect. Review important work carefully and rely on licensed professionals for decisions.',
      ] },
      { h: '9. Availability and changes', p: [
        'We work to keep ACT available and your work safe — tasks resume after interruptions, and data is backed up nightly — but the service is provided “as is” and may change or be interrupted. We will post changes to these terms on this page and update the date above.',
      ] },
      { h: '10. Ending your use', p: [
        'You can stop using ACT and delete your account at any time in Settings → Privacy & memory. We may suspend or close accounts that break these terms.',
      ] },
      { h: '11. Contact', p: ['Questions about these terms: ' + CONTACT + '.'] },
    ],
  },
  zh: {
    title: '服务条款',
    updated: UPDATED_ZH,
    intro: '本条款适用于你对 ACT（包括 AI 助手维拉）的使用。请仔细阅读，尤其是第 1、2 条。',
    sections: [
      { h: '1. ACT 是什么、不是什么', p: [
        '维拉是人工智能助手。她不是律师，也不是医生；在持证专业人士审阅之前，她写的任何内容都不构成针对你具体情况的法律或医疗意见。',
        '使用 ACT 本身并不建立律师与委托人、医生与患者之间的关系。只有当持证律师或医生明确同意代理或诊治你时，这种关系才成立。',
        '需要执照的行为——出庭、向法院提交文书、开具检查、开具处方——只会由持证专业人士执行或经其批准后执行。其中部分行为依赖尚未接通的外部服务；如果某项操作实际上没有执行，ACT 会明确告诉你。',
      ] },
      { h: '2. 紧急情况', p: [
        'ACT 不是急救服务。如果你或他人可能处于危险之中，请立即拨打 120（中国大陆）、911（美国）或当地急救电话。在美国，也可拨打或发短信至 988 自杀与危机求助热线。',
      ] },
      { h: '3. 谁可以使用 ACT', p: [
        '你必须年满 18 周岁并具备订立合同的能力。你需对告诉维拉的信息的准确性负责——她的工作质量取决于她获得的事实。',
      ] },
      { h: '4. 你的账户', p: [
        '请妥善保管密码；如果你认为账户被他人未经许可访问，请发送邮件至 ' + CONTACT + '。你可以在“设置 → 安全”中查看并退出已登录的设备。',
      ] },
      { h: '5. 审批由你决定', p: [
        '信件、邮件和各类申请只有在合适的人批准其确切内容之后才会发出。你批准某项内容，即表示你已阅读过它。消息一经发出或文书一经提交，便无法撤回。',
      ] },
      { h: '6. 可接受的使用', p: [
        '不得利用 ACT 从事违法行为；不得欺骗法院、伪造或销毁证据、教唆作伪证；不得借此获取管制药品；不得骚扰他人；不得干扰服务（包括自动化或过度使用）。维拉会拒绝此类请求，我们也可能暂停试图这样做的账户。',
      ] },
      { h: '7. 你的内容', p: [
        '你上传的内容以及为你生成的工作成果，所有权归你。你允许 ACT 仅在为你提供服务所必需的范围内存储和处理这些内容，具体见《隐私政策》。你可以随时导出或删除它们。',
      ] },
      { h: '8. AI 的局限', p: [
        'AI 可能出错、不完整或信息过时。ACT 会将判例引用与公开法院记录核对，并将药物与公开的药品说明书核对，但任何核查都不是完美的。请仔细审阅重要内容，并以持证专业人士的判断作为决策依据。',
      ] },
      { h: '9. 可用性与变更', p: [
        '我们努力保持 ACT 可用并保护你的工作——任务在中断后会自动继续，数据每晚备份——但服务按“现状”提供，可能变更或中断。条款如有变更，我们会在本页公布并更新上方日期。',
      ] },
      { h: '10. 停止使用', p: [
        '你可以随时停止使用 ACT，并在“设置 → 隐私与记忆”中删除账户。对于违反本条款的账户，我们可能暂停或关闭。',
      ] },
      { h: '11. 联系我们', p: ['关于本条款的问题：' + CONTACT + '。'] },
    ],
  },
}

export const privacy: Record<'en' | 'zh', Doc> = {
  en: {
    title: 'Privacy Policy',
    updated: UPDATED_EN,
    intro: 'You may tell Vera things you tell almost no one. This page explains, plainly, what ACT keeps, where it goes, and what you can do about it.',
    sections: [
      { h: 'What we collect', p: [
        'Account: your name, email address, and a one-way hash of your password (we never store the password itself).',
        'What you share and what is made for you: your conversations with Vera, the text extracted from files you upload (the original file is not kept), your cases, and the documents, plans and reminders produced for them.',
        'What Vera remembers: short facts you have shared (for example your state, or an allergy), only while memory is switched on. You can see and delete each one in Settings → Privacy & memory.',
        'Settings and usage: your preferences, your signed-in devices (browser type and IP address), and how much AI processing your account used.',
      ] },
      { h: 'Where your information goes', p: [
        'Hosting: ACT runs on servers and a database operated for us on Amazon Web Services in the United States.',
        'AI processing: to understand your messages and do the work, your conversations and relevant case material are sent to an AI model service — currently Alibaba Cloud Model Studio (Qwen), processed in Beijing, China. This is an international transfer of your information.',
        'Public research sources: when Vera researches, search terms such as a legal question or a medication name are sent to public services — CourtListener (Free Law Project), openFDA (U.S. Food and Drug Administration), the U.S. National Library of Medicine (RxNorm, Clinical Tables, PubMed) and Cornell’s Legal Information Institute. Your name and account are not sent.',
        'Licensed professionals: when an action needs a licensed attorney’s or physician’s approval, the professional reviewing it sees the case material needed to decide.',
        'Email: account and notification emails are sent through ACT’s own mail server. Notification emails do not contain the content of your case.',
        'Fonts: the site loads typefaces from Google Fonts, which receives your IP address when the page loads.',
        'We do not sell your information, and we do not use it for advertising.',
      ] },
      { h: 'Cookies and local storage', p: [
        'ACT uses one essential cookie to keep you signed in. Your browser also remembers your language choice. There are no advertising or analytics cookies.',
      ] },
      { h: 'How long we keep it', p: [
        'We keep your information while your account exists. When you delete your account, your account and everything in it — conversations, cases, documents, memories — are deleted from the live database. Copies may remain in backups for a limited period until those backups are replaced.',
      ] },
      { h: 'How we protect it', p: [
        'Connections are encrypted with HTTPS. Passwords are hashed with Argon2id; session and email-link tokens are stored only as hashes. Each client’s cases and documents are available only to that client and, for an action awaiting approval, to the licensed professionals who may decide it. Every action taken on a case is recorded on its timeline.',
      ] },
      { h: 'Your choices and rights', p: [
        'See and correct your profile and preferences in Settings. Turn Vera’s memory off, or delete what she remembers. Download everything in your account (Settings → Privacy & memory → Export). Delete your account at any time. Turn notification emails off in Settings → Notifications.',
        'For any other request about your information, email ' + CONTACT + '.',
      ] },
      { h: 'Children', p: ['ACT is not intended for anyone under 18, and we do not knowingly collect information from children.'] },
      { h: 'Changes', p: ['If this policy changes, we will post the new version here and update the date above.'] },
    ],
  },
  zh: {
    title: '隐私政策',
    updated: UPDATED_ZH,
    intro: '你可能会告诉维拉一些几乎不对别人说的事。本页用平实的话说明：ACT 保存什么、信息去了哪里、你能做什么。',
    sections: [
      { h: '我们收集什么', p: [
        '账户信息：你的姓名、邮箱地址，以及密码的单向哈希值（我们从不保存密码本身）。',
        '你分享的内容和为你生成的内容：你与维拉的对话、从你上传的文件中提取的文字（不保留原始文件）、你的案件，以及为案件生成的文书、方案和提醒。',
        '维拉记住的内容：你分享过的简短事实（例如所在州或过敏史），仅在记忆功能开启时保存。你可以在“设置 → 隐私与记忆”中逐条查看和删除。',
        '设置与用量：你的偏好、已登录的设备（浏览器类型和 IP 地址），以及账户使用的 AI 处理量。',
      ] },
      { h: '你的信息会去哪里', p: [
        '托管：ACT 运行在为我们托管于美国亚马逊云（Amazon Web Services）上的服务器和数据库中。',
        'AI 处理：为理解你的消息并完成工作，你的对话及相关案件材料会发送给 AI 模型服务——目前为阿里云百炼（通义千问），在中国北京处理。这属于信息的跨境传输。',
        '公开检索来源：维拉检索时，法律问题或药物名称等检索词会发送给公开服务——CourtListener（Free Law Project）、openFDA（美国食品药品监督管理局）、美国国家医学图书馆（RxNorm、Clinical Tables、PubMed）以及康奈尔大学法律信息研究所。你的姓名和账户信息不会被发送。',
        '持证专业人士：当某项操作需要持证律师或医生批准时，负责审阅的专业人士会看到作出决定所需的案件材料。',
        '邮件：账户和通知邮件通过 ACT 自己的邮件服务器发送。通知邮件不包含你的案件内容。',
        '字体：网站从 Google Fonts 加载字体，页面加载时 Google 会收到你的 IP 地址。',
        '我们不出售你的信息，也不将其用于广告。',
      ] },
      { h: 'Cookie 与本地存储', p: [
        'ACT 只使用一个保持登录所必需的 Cookie。你的浏览器还会记住你选择的语言。没有广告或分析类 Cookie。',
      ] },
      { h: '保存多久', p: [
        '账户存续期间我们会保存你的信息。你删除账户后，账户及其中的全部内容——对话、案件、文书、记忆——都会从在线数据库中删除。备份中可能在有限期间内保留副本，直到这些备份被替换。',
      ] },
      { h: '我们如何保护', p: [
        '连接通过 HTTPS 加密。密码使用 Argon2id 哈希；会话令牌和邮件链接令牌只以哈希形式保存。每位客户的案件和文书只对其本人可见；对于等待审批的操作，也对有权作出决定的持证专业人士可见。案件上的每一项操作都会记录在其时间线上。',
      ] },
      { h: '你的选择与权利', p: [
        '在“设置”中查看并更正个人资料和偏好；关闭维拉的记忆，或删除她记住的内容；下载账户中的全部数据（设置 → 隐私与记忆 → 导出）；随时删除账户；在“设置 → 通知”中关闭通知邮件。',
        '关于你的信息的其他请求，请发送邮件至 ' + CONTACT + '。',
      ] },
      { h: '未成年人', p: ['ACT 不面向未满 18 周岁的人士，我们不会有意收集儿童的信息。'] },
      { h: '变更', p: ['本政策如有变更，我们会在此公布新版本并更新上方日期。'] },
    ],
  },
}
