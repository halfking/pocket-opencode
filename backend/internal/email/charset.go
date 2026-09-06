package email

import (
	"golang.org/x/text/encoding/simplifiedchinese"
)

// charset.go — 中文历史编码兜底解码。企业邮箱老系统仍会用 GBK 发信，
// 标准库 mime.WordDecoder 只认 UTF-8/ISO-8859-1，这里补 GBK 系。

func gbkToUTF8(data []byte) ([]byte, error) {
	return simplifiedchinese.GB18030.NewDecoder().Bytes(data)
}
