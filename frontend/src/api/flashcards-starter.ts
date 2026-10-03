/**
 * flashcards-starter.ts — 内置学习库的 HTTP 入口。
 *
 * 为什么不放在 services/flashcards.ts：那个文件的导出签名被
 * src/services/__tests__/flashcards.contract.test.ts 逐个锁定（docs/flashcards-contract.md §2）。
 * 内置学习库是后加的功能（POST /api/flashcards/starter/import），与闪卡同步契约
 * 无关，混进去要么改契约、要么被契约测试判红。单独一个模块更诚实。
 */
import { http } from './http'

/** 内置牌组目录条目。 */
export interface StarterDeckSummary {
  deckId: string
  name: string
  description: string
  tags: string[]
  cardCount: number
  newPerDay: number
  reviewsPerDay: number
}

export interface StarterDeckListResponse {
  decks: StarterDeckSummary[]
  /** 该用户是否已经导入过（后端按 flashcard_deck_config 判定）。 */
  imported: boolean
}

export interface StarterImportResponse {
  decks: number
  cardsCreated: number
  cardsSkipped: number
  deckIds: string[]
}

export const flashcardsStarterApi = {
  async list(): Promise<StarterDeckListResponse> {
    return http<StarterDeckListResponse>('/api/flashcards/starter')
  },

  /** 一键导入内置学习库（幂等）。省略 deckIds 即全部导入。 */
  async importAll(deckIds?: string[]): Promise<StarterImportResponse> {
    return http<StarterImportResponse>('/api/flashcards/starter/import', {
      method: 'POST',
      body: JSON.stringify(deckIds && deckIds.length ? { deckIds } : {}),
    })
  },
}
