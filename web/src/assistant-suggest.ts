import type { Lang } from './locales'
import type { BoardInfo, LANOutgoing, LANState, Monitor } from './types'

/*
 * What 波西米亚 offers to talk about, chosen each time the panel opens: first what is going on right now (a failing
 * service, the page being looked at, a colleague's board), then a rotating sample of what it can do, so the four
 * starters show its range instead of the same lines every day.
 */

export type Suggestion = { kind: string; tone: 'sky' | 'rose' | 'mint' | 'yellow'; text: string }

type Context = {
  monitors: Monitor[]
  boardList: BoardInfo[]
  lan?: LANState
  remoteBoards: LANOutgoing[]
  page: string
  lang?: Lang
  guest?: boolean
}

const skillsZH: Suggestion[] = [
  { kind: '现状', tone: 'sky', text: '现在整体情况怎么样？有没有需要我处理的？' },
  { kind: '排查', tone: 'rose', text: '最近 7 天哪个服务出过问题？持续了多久，为什么？' },
  { kind: '总结', tone: 'sky', text: '把最近 24 小时的异常整理成一段可以直接发给同事的说明' },
  { kind: '稳定性', tone: 'rose', text: '这周哪个服务最不稳定？和上周比怎么样？' },
  { kind: '维护', tone: 'yellow', text: '今晚 22 点到 23 点要维护，先把相关的告警静音' },
  { kind: '整理', tone: 'mint', text: '新建一个“网站”面板，把所有网站类的服务放进去' },
  { kind: '样式', tone: 'mint', text: '把第一个面板的卡片都换成可用率环' },
  { kind: '说明', tone: 'mint', text: '给还没有说明的服务各补一句它是做什么的' },
  { kind: '告警', tone: 'yellow', text: '正在触发的告警有哪些？处理过的帮我确认掉' },
  { kind: '内网', tone: 'sky', text: '谁能看我的面板？分别能看哪个？' },
  { kind: '内网', tone: 'sky', text: '内网里有哪些面板我还没申请？帮我都申请一下' },
  { kind: '账户', tone: 'yellow', text: '帮我把头像随机换一个' },
  { kind: '用法', tone: 'yellow', text: 'lgmon 的关系图怎么看？哪些关系最值得写上？' },
]

const skillsGuestZH: Suggestion[] = [
  { kind: '现状', tone: 'sky', text: '现在整体服务状态怎么样？有没有异常？' },
  { kind: '排查', tone: 'rose', text: '最近 7 天哪个服务出过问题？持续了多久？' },
  { kind: '总结', tone: 'sky', text: '帮我总结一下最近 24 小时的服务运行情况' },
  { kind: '稳定性', tone: 'rose', text: '这周哪个服务最不稳定？可用率是多少？' },
  { kind: '用法', tone: 'yellow', text: 'lgmon 的关系图怎么看？' },
]

const skillsEN: Suggestion[] = [
  { kind: 'Status', tone: 'sky', text: 'How is everything looking right now? Does anything need attention?' },
  {
    kind: 'Debug',
    tone: 'rose',
    text: 'Which services had incidents in the last 7 days, for how long, and why?',
  },
  {
    kind: 'Summary',
    tone: 'sky',
    text: 'Summarize the last 24 hours of incidents so I can share it with my team',
  },
  { kind: 'Uptime', tone: 'rose', text: 'Which service was least stable this week compared to last week?' },
  {
    kind: 'Silence',
    tone: 'yellow',
    text: 'Silence related alerts tonight from 22:00 to 23:00 for maintenance',
  },
  { kind: 'Boards', tone: 'mint', text: 'Create a "Websites" board and put all web services in it' },
  { kind: 'Style', tone: 'mint', text: 'Switch all cards on the first board to the uptime ring style' },
  {
    kind: 'Notes',
    tone: 'mint',
    text: 'Add a short description to every service that does not have one yet',
  },
  {
    kind: 'Alerts',
    tone: 'yellow',
    text: 'Which alerts are firing right now? Acknowledge the ones already handled',
  },
  { kind: 'LAN', tone: 'sky', text: 'Who can view my boards, and which boards can they see?' },
  { kind: 'Profile', tone: 'yellow', text: 'Shuffle my avatar to a random built-in one' },
  { kind: 'Guide', tone: 'yellow', text: 'How do I read the dependency map in lgmon?' },
]

const skillsGuestEN: Suggestion[] = [
  { kind: 'Status', tone: 'sky', text: 'How are all services doing right now? Is anything down?' },
  {
    kind: 'History',
    tone: 'rose',
    text: 'Which services had incidents in the last 7 days and how long did they last?',
  },
  { kind: 'Summary', tone: 'sky', text: 'Give me a quick summary of the last 24 hours of uptime' },
  { kind: 'Uptime', tone: 'rose', text: 'Which service had the lowest uptime this week?' },
  { kind: 'Guide', tone: 'yellow', text: 'How do I read the dependency map in lgmon?' },
]

function pick<T>(items: T[], count: number, random: () => number) {
  const pool = [...items]
  const chosen: T[] = []
  while (pool.length && chosen.length < count)
    chosen.push(pool.splice(Math.floor(random() * pool.length), 1)[0])
  return chosen
}

export function suggestions(context: Context, random: () => number = Math.random): Suggestion[] {
  const en = context.lang === 'en'
  const lead: Suggestion[] = []
  const down = context.monitors.filter((monitor) => monitor.state === 'down')
  if (down[0])
    lead.push(
      en
        ? {
            kind: 'Down',
            tone: 'rose',
            text: `Why is "${down[0].name}" down, and when did it start?`,
          }
        : {
            kind: '异常',
            tone: 'rose',
            text: '「' + down[0].name + '」为什么异常？从什么时候开始的？',
          },
    )
  const [section, id] = context.page
    .replace(/^#\/?/, '')
    .split('/')
    .map((part) => decodeURIComponent(part || ''))
  if (section === 'monitors' && id) {
    const monitor = context.monitors.find((item) => item.id === id)
    if (monitor) {
      lead.push(
        en
          ? {
              kind: 'Service',
              tone: 'sky',
              text: `Has "${monitor.name}" had any issues in the last 7 days?`,
            }
          : {
              kind: '这个服务',
              tone: 'sky',
              text: '「' + monitor.name + '」最近 7 天出过问题吗？',
            },
      )
      if (!monitor.hasDoc && !context.guest)
        lead.push(
          en
            ? {
                kind: 'Docs',
                tone: 'mint',
                text: `Draft documentation for "${monitor.name}" based on what you know`,
              }
            : {
                kind: '文档',
                tone: 'mint',
                text: '根据你知道的，给「' + monitor.name + '」起个文档草稿',
              },
        )
    }
  }
  if (!context.guest && ((section === 'boards' && id) || !section)) {
    const board = context.boardList.find((item) => item.id === id) || context.boardList[0]
    if (board && board.summary.services > 1)
      lead.push(
        en
          ? {
              kind: 'Board',
              tone: 'mint',
              text: `How are services on "${board.name}" connected? Fill in missing dependencies`,
            }
          : {
              kind: '这个面板',
              tone: 'mint',
              text: '「' + board.name + '」里的服务之间是什么关系？缺的依赖帮我补上',
            },
      )
  }
  if (!context.guest && context.remoteBoards[0])
    lead.push(
      en
        ? {
            kind: 'Team',
            tone: 'sky',
            text: `How is ${context.remoteBoards[0].ownerName}'s "${context.remoteBoards[0].boardName}" board right now?`,
          }
        : {
            kind: '同事',
            tone: 'sky',
            text:
              context.remoteBoards[0].ownerName +
              ' 的「' +
              context.remoteBoards[0].boardName +
              '」现在怎么样？',
          },
    )
  const pool = en ? (context.guest ? skillsGuestEN : skillsEN) : context.guest ? skillsGuestZH : skillsZH
  const first = lead.slice(0, 2)
  const rest = pool.filter((item) => !first.some((chosen) => chosen.text === item.text))
  return [...first, ...pick(rest, 4 - first.length, random)]
}
