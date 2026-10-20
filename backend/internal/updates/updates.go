package updates

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// VersionInfo 版本信息
type VersionInfo struct {
	Version     string    `json:"version"`     // 版本号 如 "1.2.0"
	BuildNumber int       `json:"buildNumber"` // 构建号
	ReleaseDate time.Time `json:"releaseDate"` // 发布日期
	DownloadURL string    `json:"downloadUrl"` // APK 下载地址
	FileSize    int64     `json:"fileSize"`    // 文件大小（字节）
	MD5         string    `json:"md5"`         // MD5 校验
	Changelog   []string  `json:"changelog"`   // 更新日志
	ForceUpdate bool      `json:"forceUpdate"` // 是否强制更新
	MinVersion  string    `json:"minVersion"`  // 最低兼容版本
}

// CheckUpdateRequest 检查更新请求
type CheckUpdateRequest struct {
	CurrentVersion string `json:"currentVersion"`
	Platform       string `json:"platform"` // android, ios
	DeviceModel    string `json:"deviceModel"`
}

// CheckUpdateResponse 检查更新响应
type CheckUpdateResponse struct {
	HasUpdate   bool         `json:"hasUpdate"`
	Latest      *VersionInfo `json:"latest,omitempty"`
	ForceUpdate bool         `json:"forceUpdate"`
	Message     string       `json:"message"`
}

// UpdateHandler 更新检查处理器
type UpdateHandler struct {
	latestVersion *VersionInfo
}

// NewUpdateHandler 创建更新处理器
func NewUpdateHandler() *UpdateHandler {
	return &UpdateHandler{
		latestVersion: &VersionInfo{
			Version:     "1.2.0",
			BuildNumber: 2,
			ReleaseDate: time.Now(),
			DownloadURL: "http://14.103.169.56:8088/downloads/opencode-pocket-v1.2.0.apk",
			FileSize:    4200000, // 4.2 MB
			MD5:         "abc123def456",
			Changelog: []string{
				"✨ 全新移动端 UI 设计",
				"✨ 添加登录系统",
				"✨ 支持服务器选择",
				"✨ 任务按状态分组",
				"✨ 实时更新支持",
				"🐛 修复若干已知问题",
			},
			ForceUpdate: false,
			MinVersion:  "1.0.0",
		},
	}
}

// SetLatestVersion 设置最新版本信息
func (h *UpdateHandler) SetLatestVersion(version *VersionInfo) {
	h.latestVersion = version
}

// HandleCheckUpdate 处理检查更新请求
func (h *UpdateHandler) HandleCheckUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CheckUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// 比较版本
	hasUpdate := compareVersion(req.CurrentVersion, h.latestVersion.Version)

	resp := CheckUpdateResponse{
		HasUpdate:   hasUpdate,
		ForceUpdate: h.latestVersion.ForceUpdate,
		Message:     "当前已是最新版本",
	}

	if hasUpdate {
		resp.Latest = h.latestVersion
		resp.Message = "发现新版本"
	}

	writeJSON(w, http.StatusOK, resp)
}

// writeJSON 写 JSON 应答。
//
// ⚠ §946：**先编码、后写状态行**。
//
// 　实测：`json.Encoder.Encode` 先把值 marshal 进内部 buffer、成功后才写
// 　⇒ ⇒⇒ 编码失败时**响应体是空的** ⇒ ⇒⇒ 而旧写法**先 WriteHeader 了**
// 　⇒ ⇒⇒⇒ 客户端收到「200 + 空体」⇒ ⇒⇒⇒ 服务端**一个字都没留**。
// 　⇒ 改成先编码 ⇒ 编码失败时状态行还没提交 ⇒ 还能给一个说得清的 500。
//
// ⚠ 字节与旧实现逐字相同（§946 实测：Marshal+"\n" == Encoder.Encode）。
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		log.Printf("[ERROR] 版本检查应答编码失败（原状态码 %d）：%v", status, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		// ⚠ 用**常量字节**：编码失败的原因可能就是值本身不可编码 ⇒
		//　 兜底体必须**保证**写得出去。
		if _, werr := w.Write([]byte(`{"error":"response_encode_failed: failed to encode JSON response body"}` + "\n")); werr != nil {
			log.Printf("[WARN] 版本检查应答兜底体写出失败：%v", werr)
		}
		return
	}
	body = append(body, '\n')
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		log.Printf("[WARN] 版本检查应答写出失败：%v", err)
	}
}

// HandleDownloadAPK 处理 APK 下载
func (h *UpdateHandler) HandleDownloadAPK(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	version := r.URL.Query().Get("version")
	if version == "" {
		version = h.latestVersion.Version
	}

	// 实际实现中应该从文件系统或云存储读取 APK 文件
	apkPath := "/data/www/pocket.kxpms.cn/downloads/opencode-pocket-latest.apk"

	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", "attachment; filename=opencode-pocket.apk")

	http.ServeFile(w, r, apkPath)
}

// compareVersion 简单的版本比较
// 返回 true 如果 latest > current
func compareVersion(current, latest string) bool {
	// 简化实现：直接字符串比较
	// 实际应该解析版本号进行数值比较
	return latest > current
}
