package email

// Provider 定义邮件服务商的配置（IMAP + OAuth2 参数）。
type Provider struct {
	ID              string
	DisplayName     string
	IMAPHost        string
	IMAPPort        int
	SupportsOAuth2  bool
	OAuth2AuthURL   string
	OAuth2TokenURL  string
	OAuth2Scopes    []string
}

// 预定义的 7 个主流邮件服务商。
var providers = []Provider{
	{
		ID:             "gmail",
		DisplayName:    "Gmail",
		IMAPHost:       "imap.gmail.com",
		IMAPPort:       993,
		SupportsOAuth2: true,
		OAuth2AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
		OAuth2TokenURL: "https://oauth2.googleapis.com/token",
		OAuth2Scopes:   []string{"https://mail.google.com/"},
	},
	{
		ID:             "outlook",
		DisplayName:    "Outlook",
		IMAPHost:       "outlook.office365.com",
		IMAPPort:       993,
		SupportsOAuth2: true,
		OAuth2AuthURL:  "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
		OAuth2TokenURL: "https://login.microsoftonline.com/common/oauth2/v2.0/token",
		OAuth2Scopes:   []string{"https://outlook.office.com/IMAP.AccessAsUser.All", "offline_access"},
	},
	{
		ID:             "qq",
		DisplayName:    "QQ邮箱",
		IMAPHost:       "imap.qq.com",
		IMAPPort:       993,
		SupportsOAuth2: false,
	},
	{
		ID:             "163",
		DisplayName:    "网易163",
		IMAPHost:       "imap.163.com",
		IMAPPort:       993,
		SupportsOAuth2: false,
	},
	{
		ID:             "126",
		DisplayName:    "网易126",
		IMAPHost:       "imap.126.com",
		IMAPPort:       993,
		SupportsOAuth2: false,
	},
	{
		ID:             "aliyun",
		DisplayName:    "阿里云邮箱",
		IMAPHost:       "imap.aliyun.com",
		IMAPPort:       993,
		SupportsOAuth2: false,
	},
	{
		ID:             "custom",
		DisplayName:    "自定义IMAP",
		IMAPHost:       "",
		IMAPPort:       993,
		SupportsOAuth2: false,
	},
}

// ListProviders 返回所有预定义的服务商。
func ListProviders() []Provider {
	return providers
}

// LookupProviderByID 根据 ID 查找服务商。
func LookupProviderByID(id string) (Provider, bool) {
	for _, p := range providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}
