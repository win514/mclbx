package main

import (
	"crypto/sha256"
	"strings"
	"testing"
)

// 指纹与身份证明是安全边界，须单独测试。

func TestFingerprintRoundTrip(t *testing.T) {
	id, err := newIdentity()
	if err != nil {
		t.Fatalf("生成身份失败: %v", err)
	}
	text := id.fingerprint() // AA:BB:...

	// 各种书写格式都应解析为同一值
	for _, variant := range []string{
		text,
		strings.ToLower(text),
		strings.ReplaceAll(text, ":", ""),
		strings.ReplaceAll(text, ":", " "),
		strings.ReplaceAll(text, ":", "-"),
	} {
		got, err := parseFingerprint(variant)
		if err != nil {
			t.Fatalf("解析 %q 失败: %v", variant, err)
		}
		if got != id.fp {
			t.Errorf("解析 %q 得到 %x，期望 %x", variant, got, id.fp)
		}
	}
}

func TestFingerprintRejectsGarbage(t *testing.T) {
	for _, bad := range []string{
		"",
		"zz",
		"0011",                          // 太短
		strings.Repeat("ab", 33),        // 太长
		strings.Repeat("ab", 32) + "zz", // 非十六进制
	} {
		if _, err := parseFingerprint(bad); err == nil {
			t.Errorf("垃圾输入 %q 居然通过了", bad)
		}
	}
}

// 安全码须与参数顺序无关。
func TestSafetyCodeIsSymmetric(t *testing.T) {
	a := sha256.Sum256([]byte("host-cert"))
	b := sha256.Sum256([]byte("guest-cert"))

	if safetyCode(a, b) != safetyCode(b, a) {
		t.Errorf("安全码与参数顺序有关，两端算不出同一个值")
	}
	c := sha256.Sum256([]byte("someone-else"))
	if safetyCode(a, b) == safetyCode(a, c) {
		t.Errorf("换了证书安全码却没变")
	}
	if len(safetyCode(a, b)) != 9 {
		t.Errorf("安全码长度异常: %q", safetyCode(a, b))
	}
}

// verifyProof 是身份绑定的核心检查点。
func TestVerifyProof(t *testing.T) {
	id, err := newIdentity()
	if err != nil {
		t.Fatalf("生成身份失败: %v", err)
	}
	ch := idChallenge(id.fp[:], id.fp[:], []byte("nonce-g"), []byte("nonce-h"))
	sig, err := id.sign(ch)
	if err != nil {
		t.Fatalf("签名失败: %v", err)
	}
	proof := &idMsg{Cert: id.cert.Certificate[0], Sig: sig}

	// 1) 正常 → 通过
	if err := verifyProof(proof, ch, id.fp, "对端"); err != nil {
		t.Errorf("正常证明却被拒绝: %v", err)
	}

	// 2) 指纹被改 → 拒绝
	var tampered [32]byte
	copy(tampered[:], id.fp[:])
	tampered[0] ^= 0xFF
	if err := verifyProof(proof, ch, tampered, "对端"); err == nil {
		t.Errorf("指纹不匹配居然通过了——绑定失效")
	}

	// 3) 换挑战（防重放）→ 拒绝
	ch2 := idChallenge(id.fp[:], id.fp[:], []byte("other-g"), []byte("other-h"))
	if err := verifyProof(proof, ch2, id.fp, "对端"); err == nil {
		t.Errorf("换掉挑战居然通过了——签名没有绑到本次会话")
	}

	// 4) 证书与签名不匹配 → 拒绝
	other, err := newIdentity()
	if err != nil {
		t.Fatalf("生成第二份身份失败: %v", err)
	}
	fake := &idMsg{Cert: other.cert.Certificate[0], Sig: sig}
	if err := verifyProof(fake, ch, other.fp, "对端"); err == nil {
		t.Errorf("私钥与证书不匹配居然通过了——证明持有私钥这一步失效")
	}

	// 5) 空证明 → 拒绝
	if err := verifyProof(&idMsg{}, ch, id.fp, "对端"); err == nil {
		t.Errorf("空证明居然通过了")
	}
}

// 挑战须同时含双方指纹与随机数，否则可被重放。
func TestChallengeBindsEverything(t *testing.T) {
	var a, b [32]byte
	a[0], b[0] = 1, 2
	base := idChallenge(a[:], b[:], []byte("g"), []byte("h"))

	if string(base) == string(idChallenge(b[:], a[:], []byte("g"), []byte("h"))) {
		t.Errorf("调换双方指纹挑战没变——角色没有绑定进去")
	}
	if string(base) == string(idChallenge(a[:], b[:], []byte("g2"), []byte("h"))) {
		t.Errorf("换掉客户机随机数挑战没变——可被重放")
	}
	if string(base) == string(idChallenge(a[:], b[:], []byte("g"), []byte("h2"))) {
		t.Errorf("换掉主机随机数挑战没变——可被重放")
	}
}
