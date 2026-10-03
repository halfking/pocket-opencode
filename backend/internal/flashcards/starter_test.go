package flashcards

import (
	"context"
	"strings"
	"testing"
)

// TestStarterDecksAreValid 直接对随二进制打包的真实数据跑校验。
//
// 这一条的意义是"编译进二进制的数据"也必须有牙齿：如果某天有人提交了
// 一份缺背面/重复 id 的牌组，这里立刻红，而不是等用户导入后看到空卡。
func TestStarterDecksAreValid(t *testing.T) {
	decks, err := StarterDecks()
	if err != nil {
		t.Fatalf("StarterDecks: %v", err)
	}
	if len(decks) < 4 {
		t.Fatalf("expected at least 4 starter decks (AI / 智能体 / 英语单词 / 常用句), got %d", len(decks))
	}
	byID := map[string]StarterDeck{}
	for _, d := range decks {
		if _, dup := byID[d.DeckID]; dup {
			t.Fatalf("duplicate deck id %q", d.DeckID)
		}
		byID[d.DeckID] = d
		if len(d.Cards) < 20 {
			t.Errorf("deck %q only has %d cards", d.DeckID, len(d.Cards))
		}
	}
	// 用户点名要的四套：AI 知识点 / 智能体与大模型 / 英语单词+发音 / 常用句。
	for _, want := range []string{"starter-ai-basics", "starter-agent-llm", "starter-english-words", "starter-english-phrases"} {
		if _, ok := byID[want]; !ok {
			t.Errorf("missing required starter deck %q (have %v)", want, deckIDs(decks))
		}
	}
	// 英语两套必须带发音信息，否则"英语单词及发音"这条需求是空的。
	for _, d := range decks {
		if !strings.Contains(d.DeckID, "english") {
			continue
		}
		withIPA := 0
		for _, c := range d.Cards {
			if strings.HasPrefix(strings.TrimSpace(c.Back), "IPA: /") {
				withIPA++
			}
		}
		if withIPA != len(d.Cards) {
			t.Errorf("deck %q: only %d/%d cards carry IPA pronunciation", d.DeckID, withIPA, len(d.Cards))
		}
	}
}

func deckIDs(decks []StarterDeck) []string {
	out := make([]string, 0, len(decks))
	for _, d := range decks {
		out = append(out, d.DeckID)
	}
	return out
}

func TestStarterDeckSummariesMatchDecks(t *testing.T) {
	summaries, err := StarterDeckSummaries()
	if err != nil {
		t.Fatalf("StarterDeckSummaries: %v", err)
	}
	decks, _ := StarterDecks()
	if len(summaries) != len(decks) {
		t.Fatalf("summary count %d != deck count %d", len(summaries), len(decks))
	}
	for i, s := range summaries {
		if s.DeckID != decks[i].DeckID || s.CardCount != len(decks[i].Cards) {
			t.Errorf("summary %d mismatch: %+v vs deck %q(%d cards)", i, s, decks[i].DeckID, len(decks[i].Cards))
		}
	}
}

func TestValidateStarterDecksRejectsBadInput(t *testing.T) {
	good := StarterDeck{DeckID: "d", Name: "n", Cards: []StarterCard{{ID: "d-001", Front: "f", Back: "b"}}}
	if err := ValidateStarterDecks([]StarterDeck{good}); err != nil {
		t.Fatalf("valid deck rejected: %v", err)
	}
	cases := map[string]StarterDeck{
		"empty deck id":        {Name: "n", Cards: []StarterCard{{ID: "x-001", Front: "f", Back: "b"}}},
		"empty name":           {DeckID: "d", Cards: []StarterCard{{ID: "d-001", Front: "f", Back: "b"}}},
		"no cards":             {DeckID: "d", Name: "n"},
		"empty front":          {DeckID: "d", Name: "n", Cards: []StarterCard{{ID: "d-001", Back: "b"}}},
		"empty back":           {DeckID: "d", Name: "n", Cards: []StarterCard{{ID: "d-001", Front: "f"}}},
		"id not deck-prefixed": {DeckID: "d", Name: "n", Cards: []StarterCard{{ID: "other-001", Front: "f", Back: "b"}}},
	}
	for name, deck := range cases {
		if err := ValidateStarterDecks([]StarterDeck{deck}); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
	if err := ValidateStarterDecks(nil); err == nil {
		t.Error("empty starter data should be an error")
	}
	dup := []StarterDeck{good, good}
	if err := ValidateStarterDecks(dup); err == nil {
		t.Error("duplicate deck id should be an error")
	}
}

// TestImportStarterDecksIsIdempotent 锁住"内置学习库可反复导入"这一性质：
// 第二次导入必须建 0 张新卡，且**不覆盖**用户已经复习过的卡片状态。
func TestImportStarterDecksIsIdempotent(t *testing.T) {
	s, cleanup := newPgStore(t)
	defer cleanup()
	ctx := context.Background()
	const user = "u-starter"

	first, err := s.ImportStarterDecks(ctx, user, nil)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if first.Decks < 4 {
		t.Errorf("decks = %d, want >= 4", first.Decks)
	}
	if first.CardsCreated < 100 {
		t.Errorf("cards created = %d, want >= 100", first.CardsCreated)
	}
	if first.CardsSkipped != 0 {
		t.Errorf("cards skipped on first import = %d, want 0", first.CardsSkipped)
	}

	// 用户复习了一张卡（state 2 / due 变未来），再导入一次。
	decks, _ := StarterDecks()
	cardID := starterCardID(user, decks[0].Cards[0].ID)
	if _, err := s.pool.Exec(ctx, `UPDATE flashcard_cards SET state=2, due=9999999999, reps=3 WHERE id=$1 AND user_id=$2`, cardID, user); err != nil {
		t.Fatalf("simulate review: %v", err)
	}

	second, err := s.ImportStarterDecks(ctx, user, nil)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if second.CardsCreated != 0 {
		t.Errorf("second import created %d cards, want 0", second.CardsCreated)
	}
	if second.CardsSkipped != first.CardsCreated {
		t.Errorf("second import skipped %d, want %d", second.CardsSkipped, first.CardsCreated)
	}
	var state, reps int
	var due int64
	if err := s.pool.QueryRow(ctx, `SELECT state, reps, due FROM flashcard_cards WHERE id=$1 AND user_id=$2`, cardID, user).Scan(&state, &reps, &due); err != nil {
		t.Fatalf("read reviewed card: %v", err)
	}
	if state != 2 || reps != 3 || due != 9999999999 {
		t.Errorf("re-import clobbered review state: state=%d reps=%d due=%d", state, reps, due)
	}

	// 只导入一套牌组。**关键**：必须用一个全新的用户 —— 内置卡 id 是固定的，
	// 而 notes.id 是全局主键；如果派生 id 不带用户，第二个人导入会全部冲突
	// 跳过、拿到 0 张卡，而且不报错。
	one, err := s.ImportStarterDecks(ctx, "u-one", []string{"starter-ai-basics"})
	if err != nil {
		t.Fatalf("single deck import: %v", err)
	}
	if one.Decks != 1 || len(one.DeckIDs) != 1 || one.DeckIDs[0] != "starter-ai-basics" {
		t.Errorf("single deck import = %+v", one)
	}
	if one.CardsCreated != 70 {
		t.Errorf("ai deck cards for a second user = %d, want 70", one.CardsCreated)
	}
	// 同一套内置内容在两个用户下必须是两套独立行（互不影响复习进度）。
	var notesForTwoUsers int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM flashcard_notes WHERE user_id = ANY($1)`, []string{user, "u-one"}).Scan(&notesForTwoUsers); err != nil {
		t.Fatalf("count notes: %v", err)
	}
	if notesForTwoUsers != first.CardsCreated+70 {
		t.Errorf("notes across two users = %d, want %d", notesForTwoUsers, first.CardsCreated+70)
	}

	has, err := s.HasStarterDecks(ctx, user)
	if err != nil || !has {
		t.Errorf("HasStarterDecks(user) = %v, %v; want true", has, err)
	}
	hasOther, err := s.HasStarterDecks(ctx, "u-never-imported")
	if err != nil || hasOther {
		t.Errorf("HasStarterDecks(never) = %v, %v; want false", hasOther, err)
	}
}

func TestImportStarterDecksRejectsEmptyUser(t *testing.T) {
	s, cleanup := newPgStore(t)
	defer cleanup()
	if _, err := s.ImportStarterDecks(context.Background(), "", nil); err == nil {
		t.Error("empty user id must be rejected (a cross-user import would be a data leak)")
	}
}

// 内置卡必须真的能被复习查询看到，否则"建库了但复习页是空的"。
func TestStarterCardsAreDueForReview(t *testing.T) {
	s, cleanup := newPgStore(t)
	defer cleanup()
	ctx := context.Background()
	const user = "u-due"
	if _, err := s.ImportStarterDecks(ctx, user, []string{"starter-english-words"}); err != nil {
		t.Fatalf("import: %v", err)
	}
	due, err := s.CountDueCards(ctx, user, s.Now())
	if err != nil {
		t.Fatalf("CountDueCards: %v", err)
	}
	if due != 120 {
		t.Errorf("due cards = %d, want 120 (全部是 new 状态、due=导入时刻)", due)
	}
}
