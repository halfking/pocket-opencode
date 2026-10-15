package usersetting

// Decision is the LWW outcome when a client PUT meets a stored row.
type Decision int

const (
	// DecisionApply writes the client row (missing server row, or client newer).
	DecisionApply Decision = iota
	// DecisionKeep keeps the server row (server newer or equal).
	DecisionKeep
)

// DecidePut compares unix-second stamps. The later stamp wins.
// Equal stamps keep the server row so a replay does not churn updated_at.
//
// ★ 例外：显式清空（clientPut.ClearSecret）**永远应用**（2026-10-08）。
//
//	为什么：DecidePut 的等值保留是为「重放不抖动」设计的，但清空是
//	**撤销一把凭据**的用户动作，时钟分辨率不该有否决权。
//	实测缺陷：用户在设置页「保存设置」后紧接着「清除 API Key」，
//	两次落在同一秒 ⇒ 第二次被判 DecisionKeep 而**整条丢弃**
//	⇒ hasApiKey 一直为 true、转写继续拿旧凭据跑、恒 401，用户无解。
//	这个组合是**正常操作序列**，不是异常重放。
//
//	反向的代价（误清空）为什么可接受：只有服务端**已经**持有密文、
//	而客户端显式要求清空时才会被应用。若是真正的并发重放，
//	最坏结果是用户需要再输一次 key —— 远小于「永远清不掉」。
func DecidePut(clientPut Record, serverUpdated int64) Decision {
	if clientPut.ClearSecret {
		return DecisionApply
	}
	if clientPut.UpdatedAt > serverUpdated {
		return DecisionApply
	}
	return DecisionKeep
}
