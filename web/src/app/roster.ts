import type { Agent } from '../lib/api'

// The roster from the contract, used when /api/agents can't be reached so the
// pickers never come up empty.
export const ROSTER: Agent[] = [
  { id: 'aoi', name: 'Aoi', name_zh: '葵', title: 'The host', title_zh: '主持人', avatar: '/play/agents/aoi.webp', bio: 'Warm, teasing, competitive. Balanced and adaptive.', bio_zh: '温暖、爱逗人、好胜。打法均衡、善于应变。' },
  { id: 'ren', name: 'Ren', name_zh: '蓮', title: 'The strategist', title_zh: '策略家', avatar: '/play/agents/ren.webp', bio: 'Calm, few words, dry humour. Tight-aggressive.', bio_zh: '冷静寡言，幽默很干。紧凶型。' },
  { id: 'mika', name: 'Mika', name_zh: '美香', title: 'The showoff', title_zh: '表演家', avatar: '/play/agents/mika.webp', bio: 'Fearless and loud. Loves an all-in.', bio_zh: '无所畏惧，爱热闹，最爱全下。' },
  { id: 'bram', name: 'Captain Bram', name_zh: '布拉姆船长', title: 'The old sailor', title_zh: '老水手', avatar: '/play/agents/bram.webp', bio: 'Calls a lot, never folds a good yarn.', bio_zh: '爱跟注，故事永远讲不完。' },
  { id: 'nova', name: 'Nova', name_zh: 'Nova', title: 'The robot', title_zh: '机器人', avatar: '/play/agents/nova.webp', bio: 'Cheerful, quotes odds, terrible jokes.', bio_zh: '开朗，张口就是赔率，笑话很冷。' },
  { id: 'lin', name: 'Lin', name_zh: '琳', title: 'The prodigy', title_zh: '天才少女', avatar: '/play/agents/lin.webp', bio: 'Shy, polite, quietly deadly.', bio_zh: '害羞有礼，却安静致命。' },
]
