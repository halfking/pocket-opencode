package meetingagent

import "github.com/halfking/pocket-opencode/backend/internal/notes"

// 编译期钉住：*notes.Store 必须满足 NoteSource。
// 这行如果哪天失配，会在**编译时**报错，而不是等到 agent 在真机上
// 静默退回「无工具」——那时参考资料又变成空的了。
var _ NoteSource = (*notes.Store)(nil)
