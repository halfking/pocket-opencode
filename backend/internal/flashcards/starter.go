// Package flashcards — starter.go
//
// 内置学习库（四套牌组：AI 知识点 / 智能体与大模型 / 英语单词与发音 / 英语常用句）。
//
// 数据放在 starter_data/*.json 里并用 go:embed 打进二进制：这样牌组是
// "随版本走"的初始数据，而不是需要联网或读外部文件的运行时依赖，App 离线
// 也能建库。
//
// 导入是幂等的：note/card 的主键由「用户 + 内置卡 id」稳定派生，同一个用户
// 重复导入只会命中 ON CONFLICT DO NOTHING，不会重复建卡、也不会覆盖用户
// 已经复习过的状态。
//
// 派生 id 里必须带用户：flashcard_notes.id 是**全局**主键（不按 user_id 分区），
// 而内置卡 id 又是固定的。如果直接拿内置 id 当主键，第二个用户导入时每一条
// 都会撞上第一个用户的行，于是"第二个人导入内置学习库建出 0 张卡"，
// 而且没有任何报错。TestImportStarterDecksIsIdempotent 就是为此存在的。
package flashcards

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed starter_data/*.json
var starterDataFS embed.FS

// StarterCard 是一条内置卡片的源数据。
type StarterCard struct {
	ID    string   `json:"id"`
	Front string   `json:"front"`
	Back  string   `json:"back"`
	Tags  []string `json:"tags"`
}

// StarterDeck 是一套内置牌组的源数据。
type StarterDeck struct {
	DeckID        string        `json:"deckId"`
	Name          string        `json:"name"`
	Description   string        `json:"description"`
	Tags          []string      `json:"tags"`
	NewPerDay     int           `json:"newPerDay"`
	ReviewsPerDay int           `json:"reviewsPerDay"`
	Cards         []StarterCard `json:"cards"`
}

var (
	starterOnce sync.Once
	starterList []StarterDeck
	starterErr  error
)

// StarterDecks 返回内置牌组（已校验）。结果被缓存，调用方不要修改。
func StarterDecks() ([]StarterDeck, error) {
	starterOnce.Do(func() {
		entries, err := starterDataFS.ReadDir("starter_data")
		if err != nil {
			starterErr = fmt.Errorf("flashcards: read starter_data: %w", err)
			return
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
				names = append(names, e.Name())
			}
		}
		// 目录遍历顺序不保证稳定：按文件名排序让牌组顺序在任何机器上一致。
		sort.Strings(names)
		decks := make([]StarterDeck, 0, len(names))
		for _, n := range names {
			raw, err := starterDataFS.ReadFile("starter_data/" + n)
			if err != nil {
				starterErr = fmt.Errorf("flashcards: read %s: %w", n, err)
				return
			}
			var d StarterDeck
			if err := json.Unmarshal(raw, &d); err != nil {
				starterErr = fmt.Errorf("flashcards: parse %s: %w", n, err)
				return
			}
			decks = append(decks, d)
		}
		if err := ValidateStarterDecks(decks); err != nil {
			starterErr = err
			return
		}
		starterList = decks
	})
	if starterErr != nil {
		return nil, starterErr
	}
	return starterList, nil
}

// ValidateStarterDecks 检查内置数据本身是否可用。
//
// 这一层校验存在的理由：内置数据是编译进二进制的，如果某天有人提交了一份
// 缺字段 / id 撞车 / 背面为空的数据，症状会变成"用户导入后卡片是空的"，
// 而且要等到真机导入才看得见。宁可启动就报错。
func ValidateStarterDecks(decks []StarterDeck) error {
	if len(decks) == 0 {
		return fmt.Errorf("flashcards: starter data is empty")
	}
	seenDeck := map[string]bool{}
	seenCard := map[string]bool{}
	for _, d := range decks {
		if strings.TrimSpace(d.DeckID) == "" {
			return fmt.Errorf("flashcards: starter deck missing deckId")
		}
		if seenDeck[d.DeckID] {
			return fmt.Errorf("flashcards: duplicate starter deckId %q", d.DeckID)
		}
		seenDeck[d.DeckID] = true
		if strings.TrimSpace(d.Name) == "" {
			return fmt.Errorf("flashcards: starter deck %q missing name", d.DeckID)
		}
		if len(d.Cards) == 0 {
			return fmt.Errorf("flashcards: starter deck %q has no cards", d.DeckID)
		}
		for _, c := range d.Cards {
			if seenCard[c.ID] {
				return fmt.Errorf("flashcards: duplicate starter card id %q", c.ID)
			}
			seenCard[c.ID] = true
			if !strings.HasPrefix(c.ID, d.DeckID+"-") {
				return fmt.Errorf("flashcards: card id %q must be prefixed with deck id %q", c.ID, d.DeckID)
			}
			if strings.TrimSpace(c.Front) == "" || strings.TrimSpace(c.Back) == "" {
				return fmt.Errorf("flashcards: card %q has empty front or back", c.ID)
			}
		}
	}
	return nil
}

// StarterDeckSummary 是给前端列表用的摘要（不含卡片正文）。
type StarterDeckSummary struct {
	DeckID        string   `json:"deckId"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Tags          []string `json:"tags"`
	CardCount     int      `json:"cardCount"`
	NewPerDay     int      `json:"newPerDay"`
	ReviewsPerDay int      `json:"reviewsPerDay"`
}

// StarterDeckSummaries 返回内置牌组摘要。
func StarterDeckSummaries() ([]StarterDeckSummary, error) {
	decks, err := StarterDecks()
	if err != nil {
		return nil, err
	}
	out := make([]StarterDeckSummary, 0, len(decks))
	for _, d := range decks {
		out = append(out, StarterDeckSummary{
			DeckID:        d.DeckID,
			Name:          d.Name,
			Description:   d.Description,
			Tags:          d.Tags,
			CardCount:     len(d.Cards),
			NewPerDay:     d.NewPerDay,
			ReviewsPerDay: d.ReviewsPerDay,
		})
	}
	return out, nil
}

// starterNoteID / starterCardID 把「内置卡 id」派生成「该用户的行主键」。
// 用 userID 的短哈希而不是原样拼接：拼接会让主键长度随用户名线性增长，
// 哈希固定 8 位十六进制，够短且碰撞概率在这个场景可以忽略。
func starterNoteID(userID, cardID string) string {
	return cardID + "-" + shortUserHash(userID)
}

func starterCardID(userID, cardID string) string {
	return "card-" + cardID + "-" + shortUserHash(userID)
}

func shortUserHash(userID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(userID)))
	return hex.EncodeToString(sum[:4])
}

// StarterImportResult 报告一次内置学习库导入的真实结果。
type StarterImportResult struct {
	Decks        int      `json:"decks"`
	CardsCreated int      `json:"cardsCreated"`
	CardsSkipped int      `json:"cardsSkipped"`
	DeckIDs      []string `json:"deckIds"`
}

// ImportStarterDecks 把内置牌组写入指定用户的闪卡库。
//
// deckIds 为空表示全部导入。usn 从 1 开始：种子内容必须能被客户端的
// LWW 同步识别为"新数据"，usn=0 会让客户端认为本地副本才是最新的。
func (s *Store) ImportStarterDecks(ctx context.Context, userID string, deckIDs []string) (*StarterImportResult, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, fmt.Errorf("flashcards: import starter: user id is required")
	}
	decks, err := StarterDecks()
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, d := range deckIDs {
		if d = strings.TrimSpace(d); d != "" {
			want[d] = true
		}
	}
	now := s.Now()
	result := &StarterImportResult{DeckIDs: []string{}}
	for _, d := range decks {
		if len(want) > 0 && !want[d.DeckID] {
			continue
		}
		newPerDay, reviewsPerDay := d.NewPerDay, d.ReviewsPerDay
		if newPerDay <= 0 {
			newPerDay = 20
		}
		if reviewsPerDay <= 0 {
			reviewsPerDay = 100
		}
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO flashcard_deck_config
				(deck_id,user_id,name,new_per_day,reviews_per_day,usn,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,1,$6,$6)
			ON CONFLICT (deck_id,user_id) DO NOTHING`,
			d.DeckID, userID, d.Name, newPerDay, reviewsPerDay, now); err != nil {
			return nil, fmt.Errorf("flashcards: import starter deck %s: %w", d.DeckID, err)
		}
		result.Decks++
		result.DeckIDs = append(result.DeckIDs, d.DeckID)
		for _, c := range d.Cards {
			// 每条 note + 初始 card（state=0 new，due=now）一起插，与
			// flashcardsCreateNote 的语义保持一致。两侧主键都由
			// (userID, 内置卡 id) 稳定派生，重复导入即冲突跳过。
			noteID := starterNoteID(userID, c.ID)
			cardID := starterCardID(userID, c.ID)
			tag, err := s.pool.Exec(ctx, `
				INSERT INTO flashcard_notes (id,user_id,deck_id,front,back,tags,usn,created_at,updated_at)
				VALUES ($1,$2,$3,$4,$5,$6,1,$7,$7)
				ON CONFLICT (id) DO NOTHING`,
				noteID, userID, d.DeckID, c.Front, c.Back, TagsToJSON(c.Tags), now)
			if err != nil {
				return nil, fmt.Errorf("flashcards: import starter note %s: %w", noteID, err)
			}
			ctag, err := s.pool.Exec(ctx, `
				INSERT INTO flashcard_cards
					(id,note_id,user_id,deck_id,state,due,usn,created_at,updated_at)
				VALUES ($1,$2,$3,$4,0,$5,1,$5,$5)
				ON CONFLICT (id) DO NOTHING`,
				cardID, noteID, userID, d.DeckID, now)
			if err != nil {
				return nil, fmt.Errorf("flashcards: import starter card %s: %w", c.ID, err)
			}
			if tag.RowsAffected() == 0 || ctag.RowsAffected() == 0 {
				result.CardsSkipped++
				continue
			}
			result.CardsCreated++
		}
	}
	return result, nil
}

// HasStarterDecks 判断该用户是否已经导入过内置学习库（用于 UI 提示）。
func (s *Store) HasStarterDecks(ctx context.Context, userID string) (bool, error) {
	if strings.TrimSpace(userID) == "" {
		return false, fmt.Errorf("flashcards: has starter decks: user id is required")
	}
	decks, err := StarterDecks()
	if err != nil {
		return false, err
	}
	ids := make([]string, 0, len(decks))
	for _, d := range decks {
		ids = append(ids, d.DeckID)
	}
	if len(ids) == 0 {
		return false, nil
	}
	var n int
	err = s.pool.QueryRow(ctx, `SELECT count(*) FROM flashcard_deck_config WHERE user_id=$1 AND deck_id = ANY($2)`, userID, ids).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
