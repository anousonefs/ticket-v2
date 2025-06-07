package ldb

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func (l *ldb) setHeaders(req *http.Request, reqTxUUID string) *http.Request {
	now := time.Now()
	nowUnix := now.Unix()

	req.Header.Set("Authorization", l.getAccessToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("partnerId", l.partnerID)
	req.Header.Set("X-Client-Transaction-ID", reqTxUUID)
	req.Header.Set("X-Client-Transaction-Datetime", now.Format("2006-01-02T15:04:05.999+0700"))

	if strings.Contains(req.URL.Path, "/v1/qrpayment/initiate.service") {
		body, _ := io.ReadAll(req.Body)
		hash := sha256.Sum256(body)

		digest := "SHA-256=" + base64.StdEncoding.EncodeToString(hash[:])

		signature := fmt.Sprintf("digest: %s\n(request-target): %s %s\n(created): %d\nx-client-transaction-id: %s", digest, strings.ToLower(req.Method), req.URL.Path, nowUnix, reqTxUUID)
		signature = base64.StdEncoding.EncodeToString(hmac256([]byte(l.hmacKey), []byte(signature)))
		signature = fmt.Sprintf(`keyId="%s",algorithm="hs2019",created=%d,expires=%d,headers="digest (request-target) (created) x-client-transaction-id",signature="%s"`, l.keyID, nowUnix, nowUnix, signature)

		// set signature header to request
		req.Header.Set("Digest", digest)
		req.Header.Set("Signature", signature)
	}

	return req
}

func hmac256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func isDeepLinkToStr(v bool) string {
	if v {
		return "Y"
	}
	return "N"
}
