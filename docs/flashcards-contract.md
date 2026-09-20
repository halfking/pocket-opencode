# OpenPocket 闪卡模块 v1 契约（Frozen Contract for Parallel Implementation）

> 状态：**已冻结**（freeze date 2026-09-18）。所有 v1 子代理必须遵守此契约的字段名、路由路径与 JSON 形状。
> 决策锚点：见 `/Users/xutaohuang/workspace/ai/anki` Anki 仓库的 `rslib/src/scheduler/fsrs/`（集成层）、`proto/anki/notes.proto`、`rslib/sync/main.rs`。
> v1 范围：**仅本仓库原生实现**，不引入 rslib JNI、不实现 Anki sync server、不实现 .apkg 导入（这些全部留到 v2）。

---

## 1. 数据模型（PG schema，对齐 Anki 减集 + usn 墓碑）

### 1.1 表 `flashcard_notes`
最小化卡组内容字段；Anki 的 `notes` 表是 field 列表，这里用 `front`/`back` 两个标准字段起手（v2 可扩为 fields JSON 数组）。

| 字段          | 类型           | 说明                                        |
|---------------|----------------|---------------------------------------------|
| `id`          | text PK        | ULID/UUID，服务端生成                       |
| `user_id`     | text NOT NULL  | 来源 JWT claim，不接受请求体覆盖            |
| `deck_id`     | text NOT NULL  | 所属 deck                                   |
| `front`       | text NOT NULL  | 问题面                                       |
| `back`        | text NOT NULL  | 答案面                                       |
| `tags`        | text           | JSON 数组字符串，默认 `[]`                   |
| `usn`         | bigint NOT NULL DEFAULT 0 | Update Sequence Number；服务端写时 +1     |
| `created_at`  | bigint NOT NULL | 秒级 Unix 时间戳                           |
| `updated_at`  | bigint NOT NULL | 秒级 Unix 时间戳（LWW 同步用）             |
| `deleted_at`  | bigint         | 非空 = 墓碑；增量同步时通过 `ListDeleted*` 下发 |

### 1.2 表 `flashcard_cards`
对齐 Anki `cards` 表的核心排程字段；`due` 是双语义：
- **新卡 / 学习中**：`due` = 秒级 Unix 时间戳（`int64`）
- **毕业进入复习后**：`due` = 距 epoch 的天数（`int64`，与 Anki 一致便于 v2 .apkg）

| 字段             | 类型           | 说明                                   |
|------------------|----------------|----------------------------------------|
| `id`             | text PK        |                                        |
| `note_id`        | text NOT NULL FK→notes.id |                          |
| `user_id`        | text NOT NULL  |                                        |
| `deck_id`        | text NOT NULL  |                                        |
| `state`          | smallint NOT NULL | 0=new, 1=learning, 2=review, 3=relearning（对齐 Anki card queue type） |
| `due`            | bigint NOT NULL | 双语义见上                             |
| `interval_days`  | real NOT NULL DEFAULT 0 | FSRS 计算出的稳定间隔              |
| `stability`      | real NOT NULL DEFAULT 0 | FSRS S 参数                          |
| `difficulty`     | real NOT NULL DEFAULT 0 | FSRS D 参数                          |
| `reps`           | int NOT NULL DEFAULT 0   | 复习次数                           |
| `lapses`         | int NOT NULL DEFAULT 0   | 失败次数                           |
| `last_review_at` | bigint                |                                       |
| `usn`            | bigint NOT NULL DEFAULT 0 |                                    |
| `created_at`     | bigint NOT NULL        |                                       |
| `updated_at`     | bigint NOT NULL        |                                       |
| `deleted_at`     | bigint                 |                                       |

### 1.3 表 `flashcard_revlog`
每次复习写一条；保留 90 天后由 GC 任务清理（不在 v1 范围，结构先建好）。

| 字段            | 类型           | 说明                                       |
|-----------------|----------------|--------------------------------------------|
| `id`            | text PK        |                                            |
| `card_id`       | text NOT NULL  |                                            |
| `user_id`       | text NOT NULL  |                                            |
| `reviewed_at`   | bigint NOT NULL |                                            |
| `rating`        | smallint NOT NULL | 1=Again, 2=Hard, 3=Good, 4=Easy          |
| `prev_state`    | smallint NOT NULL |                                        |
| `next_state`    | smallint NOT NULL |                                        |
| `prev_interval` real         |                                            |
| `next_interval` real         |                                            |
| `elapsed_days`  | int           |                                            |

### 1.4 表 `flashcard_deck_config`
每 deck 一行；FSRS 参数可后续由用户调，v1 用默认权重。

| 字段                  | 类型           | 说明                                |
|-----------------------|----------------|-------------------------------------|
| `deck_id`             | text PK        |                                     |
| `user_id`             | text NOT NULL  |                                     |
| `name`                | text NOT NULL  |                                     |
| `new_per_day`         | int NOT NULL DEFAULT 20 |                          |
| `reviews_per_day`     | int NOT NULL DEFAULT 200 |                         |
| `learning_steps_min`  | int[] NOT NULL DEFAULT '{1,10}' | PostgreSQL int array        |
| `graduating_interval_days` | int NOT NULL DEFAULT 1 |                         |
| `easy_interval_days`  | int NOT NULL DEFAULT 4 |                              |
| `fsrs_weights`        | real[]         | 默认 17 维 FSRS-5 权重              |
| `desired_retention`   | real NOT NULL DEFAULT 0.9 |                          |
| `usn`                 | bigint NOT NULL DEFAULT 0 |                          |
| `created_at`          | bigint NOT NULL |                                    |
| `updated_at`          | bigint NOT NULL |                                    |
| `deleted_at`          | bigint          |                                     |

### 1.5 墓碑与 usn 规则
- 软删除：写 `deleted_at = now()`，`usn` 自增。
- 增量查询：`since` 参数（秒）只回 `updated_at > since` 的行；墓碑行不回主体，只通过 `ListDeleted*Since(user_id, since, limit)` 回 `deletedIds`。
- 服务端永远把 JWT claim 的 `user_id` 当权威；请求体的 `userId` 字段被忽略。

---

## 2. HTTP API（v1 路由表）

所有路由需 JWT auth（参考 `backend/internal/server/server.go:626` 的 `requireAuth` 模式）。

| Method | Path                                       | 用途                          | 入参                              | 出参                                |
|--------|--------------------------------------------|-------------------------------|-----------------------------------|-------------------------------------|
| GET    | `/api/flashcards?since=<sec>&limit=<n>`    | 增量拉取 cards+decks          | query                             | `{cards, decks, serverTimeMs, deletedIds?}` |
| GET    | `/api/flashcards/notes?since=<sec>`        | 增量拉取 notes                | query                             | `{notes, serverTimeMs, deletedIds?}` |
| POST   | `/api/flashcards/notes`                    | 创建 note（含可选 1 张初始 card） | `{deckId, front, back, tags?}` | `Note`                                |
| PATCH  | `/api/flashcards/notes/:id`                | 编辑 note                     | `{front?, back?, tags?}`          | `Note`                                |
| DELETE | `/api/flashcards/notes/:id`                | 软删除 note（级联 cards）     | -                                 | `{ok: true}`                         |
| POST   | `/api/flashcards/cards`                    | 手动建一张 extra card         | `{noteId, due?}`                  | `Card`                               |
| PATCH  | `/api/flashcards/cards/:id`                | 手动改卡（v1 仅 due/state）   | `{due?, state?}`                  | `Card`                               |
| POST   | `/api/flashcards/cards/:id/review`         | 复习评分                      | `{rating: 1\|2\|3\|4, reviewedAt?}` | `{card, log}`                       |
| GET    | `/api/flashcards/decks/:id/due?now=<sec>`  | 拉指定 deck 当前到期卡片       | query                             | `{cards: Card[], totalDue: int}`    |

**JSON 形状**（TypeScript 对应类型在 §3）：
- `Card` 与 Go 1.2 表字段一一对应，字段名 snake_case（如 `noteId`, `deckId`, `intervalDays`, `stability`, `difficulty`）
- `Note` 同 §1.1
- `DeckConfig` 同 §1.4

---

## 3. 前端 TypeScript 类型（前端代理 B 必须使用）

文件：`frontend/src/types/flashcards.ts`（由代理 B 新建，但形状必须满足）。

```ts
export type FlashcardRating = 1 | 2 | 3 | 4  // Again | Hard | Good | Easy
export type FlashcardState = 0 | 1 | 2 | 3  // new | learning | review | relearning

export interface FlashcardNote {
  id: string
  userId: string
  deckId: string
  front: string
  back: string
  tags: string[]
  usn: number
  createdAt: number
  updatedAt: number
  deletedAt?: number
}

export interface FlashcardCard {
  id: string
  noteId: string
  userId: string
  deckId: string
  state: FlashcardState
  /** 双语义：学习中为秒级时间戳，复习中为天数 */
  due: number
  intervalDays: number
  stability: number
  difficulty: number
  reps: number
  lapses: number
  lastReviewAt: number
  usn: number
  createdAt: number
  updatedAt: number
  deletedAt?: number
}

export interface FlashcardDeckConfig {
  deckId: string
  userId: string
  name: string
  newPerDay: number
  reviewsPerDay: number
  learningStepsMin: number[]
  graduatingIntervalDays: number
  easyIntervalDays: number
  fsrsWeights: number[]
  desiredRetention: number
  usn: number
  createdAt: number
  updatedAt: number
}

export interface FlashcardReviewLog {
  id: string
  cardId: string
  userId: string
  reviewedAt: number
  rating: FlashcardRating
  prevState: FlashcardState
  nextState: FlashcardState
  prevInterval: number
  nextInterval: number
  elapsedDays: number
}
```

---

## 4. FSRS 调度真相（重要 — 跨代理一致性的唯一保证）

- **v1 唯一调度真相：前端 `useFsrs.ts` 封装 `ts-fsrs`**（包名 `ts-fsrs`，v4+）。后端 Go 端**仅做存储**，不重算 FSRS；复习评分 `POST /api/flashcards/cards/:id/review` 接受前端已经算好的 `due/state/stability/difficulty/interval_days` 字段。
- 这样避免 ts-fsrs 与 go-fsrs 数值不一致问题（v1 风险点之一）。
- `useFsrs.ts` 接口必须 export：
  - `applyReview(card: FlashcardCard, rating: FlashcardRating, now: number): FlashcardCard`
  - `fuzzIntervalDays(intervalDays: number, deckDesiredRetention: number): number`（防止同刻到期）
  - `newCardSchedule(deckConfig: FlashcardDeckConfig, now: number): { due: number; state: FlashcardState; intervalDays: number }`

---

## 5. 后端模块边界（代理 A）

| 新增文件 | 仿照文件 | 职责 |
|----------|----------|------|
| `backend/internal/flashcards/types.go` | `internal/notes/note.go` | Note/Card/DeckConfig/Revlog 结构体 |
| `backend/internal/flashcards/store.go` | `internal/notes/store.go` | PG CRUD + `ListSince` + `ListDeletedSince` |
| `backend/internal/flashcards/cards.go` | - | 复习评分写入（仅持久化，不重算 FSRS） |
| `backend/internal/server/flashcards_handler.go` | `internal/server/scheduled_task_handler.go` | HTTP handlers（GET/POST/PATCH/DELETE） |
| `backend/internal/scheduledtask/executors/flashcard_review.go` | `executors/ai.go` | 每日到期复习推送（Kind = `flashcard_review`） |

**Server 集成点**（必须改，**不是** `cmd/pocketd/main.go` 直接注册）：
- `backend/internal/server/server.go`：`Server` 结构体加 `flashcardStore *flashcards.Store`；`New()` 签名加 `flashcardStore` 参数；在 `setupRoutes` 区域（参考 line 626）注册：
  ```go
  mux.HandleFunc("/api/flashcards", s.requireAuth(s.handleFlashcardsCollection))
  mux.HandleFunc("/api/flashcards/", s.requireAuth(s.handleFlashcardsItem))
  ```
- `backend/cmd/pocketd/main.go`：在 module stores 块（约 line 87 之后）调用 `flashcards.NewStore(pool)` 拿到 `flashcardStore`，传给 `server.New(...)`。

**Executor 注册**：在 `backend/internal/scheduledtask/executors/executors.go`（或等价 aggregator）的 `NewRegistry` 里追加 `executors.NewFlashcardReviewExecutor(...)`，新增 `Kind = "flashcard_review"`。

**新增 `Kind` 常量**：在 `backend/internal/scheduledtask/types.go` 的 Kind 块加 `KindFlashcardReview Kind = "flashcard_review"`。

---

## 6. 前端模块边界（代理 B、C、D）

| 新增/修改文件 | 代理 | 说明 |
|--------------|------|------|
| `frontend/src/types/flashcards.ts` | B | 按 §3 |
| `frontend/src/services/flashcards.ts` | B | 与 §2 路由一一对应 |
| `frontend/src/stores/flashcards.ts` | B | Pinia，仿 `scheduled-tasks/store.ts` 的 outbox 模式 |
| `frontend/src/features/flashcards/FlashcardListView.vue` | B | 仿 `ScheduledTaskListView.vue` |
| `frontend/src/features/flashcards/FlashcardDeckView.vue` | B | 仿 `ScheduledTaskDetailView.vue`（单 deck 卡片列表） |
| `frontend/src/features/flashcards/FlashcardReviewView.vue` | B | 仿 `ScheduledTaskEditView.vue`（核心：四档评分） |
| `frontend/src/features/flashcards/FlashcardEditView.vue` | B | 新建/编辑 note |
| `frontend/src/composables/useFsrs.ts` | B | §4 接口 |
| `frontend/src/app/router-mobile.ts` | B | 注册 `/flashcards`、`/flashcards/deck/:id`、`/flashcards/review`、`/flashcards/edit/:id?` |
| `frontend/package.json` | B | `+ "ts-fsrs": "^4.0.0"` 到 dependencies |
| `frontend/android/app/src/main/AndroidManifest.xml` | C | 加 `SCHEDULE_EXACT_ALARM` 与 `RECEIVE_BOOT_COMPLETED` |
| `frontend/src/native/localNotifications.ts` | C | 封装 `@capacitor/local-notifications` |
| `frontend/src/native/vivoBattery.ts` | C | 引导文案（纯字符串引导，非代码可解决） |
| `frontend/src/locales/{zh-CN,zh-TW,en-US,ja-JP,ko-KR,de-DE,fr-FR,es-ES,pt-BR}.json` | D | 每文件加 `"flashcards": {...}` 命名空间 |
| `frontend/src/features/flashcards/__tests__/` | D | Vitest：`useFsrs.test.ts`（覆盖新卡/复习/重来 + fuzz）+ `api.test.ts`（契约） |

**禁止越界**：
- 代理 B 不动 `frontend/android/**`、`frontend/src/locales/**`、`backend/**`
- 代理 C 不动 `frontend/src/features/**`、`frontend/src/locales/**`、`backend/**`
- 代理 D 不动 `frontend/src/features/flashcards/{Flashcard*.vue,types.ts,store.ts,api.ts}`、`backend/**`、`frontend/android/**`

---

## 7. 真机部署与验证（最终交付，串联收尾）

不在 v1 子代理责任内，由主会话在所有子代理完成后执行：
1. `cd frontend && npm run build`
2. `npx cap sync android`
3. `cd frontend/android && ./gradlew assembleDebug`
4. 部署到 vivo X Fold5 真机，验证：
   - 每日定时提醒触发
   - 复习四档评分落库（DB 中 `flashcard_revlog` 多一行）
   - 重启 App 后增量同步（since=0 与 since=N 对比）
5. 按项目流程 commit + PR

---

## 8. v2 备选（不在 v1 范围）

- rslib 经 `cargo-ndk` 编 JNI 深度复用（参考 `rslib/src/ankidroid/` 先例）
- `.apkg` 导入/导出（Anki collection package）
- 自托管 sync server（直接用 Anki 仓库自带 `rslib/sync/main.rs` 与 `docs/syncserver` Dockerfile）