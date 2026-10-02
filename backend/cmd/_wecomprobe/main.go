// Command _wecomprobe drives the live /callback/weixin endpoint using ONLY the
// Go standard library, deliberately not the repo's own wecom package — using
// the implementation under test to build the test input would make the check
// circular.
//
// The leading underscore makes the go tool ignore this directory.
package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

func env(k string) string {
	b, err := os.ReadFile(".env")
	if err != nil {
		panic(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if kk, vv, ok := strings.Cut(line, "="); ok && kk == k {
			return strings.Trim(strings.TrimSpace(vv), "'\"")
		}
	}
	return ""
}

func sign(token, ts, nonce, echostr string) string {
	parts := []string{token, ts, nonce, echostr}
	sort.Strings(parts)
	sum := sha1.Sum([]byte(strings.Join(parts, "")))
	return fmt.Sprintf("%x", sum)
}

func encrypt(msg, aesKeyB64, corpID string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(aesKeyB64 + "=")
	if err != nil {
		return "", fmt.Errorf("decode EncodingAESKey: %w", err)
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return "", err
	}
	var body bytes.Buffer
	body.Write(make([]byte, 16)) // random
	_ = binary.Write(&body, binary.BigEndian, uint32(len(msg)))
	body.WriteString(msg)
	body.WriteString(corpID)
	// PKCS#7 to the AES block size (16). Padding to 32 - as my first two
	// attempts did - produces a pad byte the server rejects as illegal
	// ("decrypt failed"), because its check is `pad > aes.BlockSize`.
	for pad := 16 - (body.Len() % 16); pad > 0 && pad < 16; pad-- {
		body.WriteByte(byte(pad))
	}
	if body.Len()%16 != 0 {
		return "", fmt.Errorf("padded length %d is not a multiple of 16", body.Len())
	}
	iv := make([]byte, 16)
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	_ = iv
	// WeCom's IV is NOT random and is NOT prefixed to the ciphertext: it is the
	// first 16 bytes of the decoded EncodingAESKey. My first probe used a random
	// IV (the WeChat-public-account style) and the server answered "decrypt
	// failed" - the server was right and the probe was wrong.
	out := make([]byte, len(body.Bytes()))
	cipher.NewCBCEncrypter(block, raw[:16]).CryptBlocks(out, body.Bytes())
	return base64.StdEncoding.EncodeToString(out), nil
}

func get(rawurl string) (int, string) {
	res, err := http.Get(rawurl)
	if err != nil {
		return 0, "ERR " + err.Error()
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func main() {
	base := os.Getenv("BASE")
	if base == "" {
		base = "http://127.0.0.1:18099"
	}
	token, aesKey, corp := env("POCKET_WECOM_TOKEN"), env("POCKET_WECOM_ENCODING_AES_KEY"), env("POCKET_WECOM_CORP_ID")
	fmt.Printf("corpID=%s aesKeyLen=%d\n", corp, len(aesKey))

	echo := "wecom-verify-ECHO-42"
	ts := fmt.Sprintf("%d", time.Now().Unix())
	nonce := "n-abc"

	fmt.Println("\n1) plaintext mode GET (WeCom console default):")
	st, b := get(fmt.Sprintf("%s/callback/weixin?echostr=%s", base, url.QueryEscape(echo)))
	fmt.Printf("   HTTP %d body=%q\n", st, b)
	fmt.Printf("   echoed verbatim (no quotes, no trailing newline): %v\n", b == echo)

	fmt.Println("\n2) secure mode GET (msg_signature + AES-encrypted echostr):")
	enc, err := encrypt(echo, aesKey, corp)
	if err != nil {
		fmt.Println("   encrypt failed:", err)
		os.Exit(1)
	}
	q := url.Values{"msg_signature": {sign(token, ts, nonce, enc)}, "timestamp": {ts}, "nonce": {nonce}, "echostr": {enc}}
	st, b = get(base + "/callback/weixin?" + q.Encode())
	fmt.Printf("   HTTP %d body=%q\n", st, b)
	fmt.Printf("   decrypted back to the original echostr: %v\n", b == echo)

	fmt.Println("\n3) negative control - tampered signature (expect 403):")
	q = url.Values{"msg_signature": {strings.Repeat("0", 40)}, "timestamp": {ts}, "nonce": {nonce}, "echostr": {enc}}
	st, b = get(base + "/callback/weixin?" + q.Encode())
	fmt.Printf("   HTTP %d %s\n", st, strings.TrimSpace(b))

	fmt.Println("\n4) negative control - signed with the WRONG token (expect 403):")
	q = url.Values{"msg_signature": {sign("wrong-token", ts, nonce, enc)}, "timestamp": {ts}, "nonce": {nonce}, "echostr": {enc}}
	st, b = get(base + "/callback/weixin?" + q.Encode())
	fmt.Printf("   HTTP %d %s\n", st, strings.TrimSpace(b))

	fmt.Println("\n5) negative control - ciphertext for a DIFFERENT corpID (expect 403, receiveid mismatch):")
	encOther, _ := encrypt(echo, aesKey, "wwSomeoneElse")
	q = url.Values{"msg_signature": {sign(token, ts, nonce, encOther)}, "timestamp": {ts}, "nonce": {nonce}, "echostr": {encOther}}
	st, b = get(base + "/callback/weixin?" + q.Encode())
	fmt.Printf("   HTTP %d %s\n", st, strings.TrimSpace(b))
}
