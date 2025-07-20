package lrcd

import (
	"crypto/sha256"
	"encoding/binary"
)

func GenerateNonce(messageID uint32, channelURI string, secret string) []byte {
	h := sha256.New()
	idBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(idBytes, messageID)
	h.Write(idBytes)
	h.Write([]byte(channelURI))
	h.Write([]byte(secret))
	return h.Sum(nil)
}
