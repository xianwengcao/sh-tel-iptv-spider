package utils

import (
	"crypto/aes"
	"crypto/md5"
)

type AESForNodejs struct {
	key [16]byte
}

func NewAESForNodejs(key []byte) *AESForNodejs {
	c := AESForNodejs{}
	c.setKey(key)
	return &c
}

func (a *AESForNodejs) setKey(key []byte) {
	a.key = md5.Sum(key)
}

func (a *AESForNodejs) Decrypt(data []byte) []byte {
	cipher, _ := aes.NewCipher(a.key[:])
	decrypted := make([]byte, len(data))
	bs := cipher.BlockSize()
	// 只处理完整分组，避免密文长度不是分组的整数倍时越界（例如对端返回了异常内容）
	for i := 0; i+bs <= len(data); i += bs {
		cipher.Decrypt(decrypted[i:i+bs], data[i:i+bs])
	}

	return UnPadding(decrypted)
}

func (a *AESForNodejs) Encrypt(data []byte) []byte {
	// AES 的分组是 16 字节。文档里写的 "AES/ECB/PKCS5Padding" 是 Java / CryptoJS 的
	// 习惯叫法，在 AES 场景下它实际等价于 PKCS7（按 16 字节分组填充）。
	//
	// 这里以前调用的是 PKCS5Padding（按 8 字节分组），会产生非 16 倍数的缓冲区，
	// 并且循环上界写成了 len(data) 而不是填充后的长度，导致
	// `slice bounds out of range` panic —— 只要明文长度 % 16 <= 7 就会崩。
	// 改用 16 字节填充后，明文长度 % 16 >= 8 的情况输出与原先完全一致。
	plain := PKCS7Padding(data)
	cipher, _ := aes.NewCipher(a.key[:])
	encrypted := make([]byte, len(plain))
	bs := cipher.BlockSize()
	for i := 0; i+bs <= len(plain); i += bs {
		cipher.Encrypt(encrypted[i:i+bs], plain[i:i+bs])
	}

	return encrypted
}
