package upstream

import (
	"encoding/base64"
	"fmt"
)

func BasicAuthHeader(username, password string) string {
	cred := fmt.Sprintf("%s:%s", username, password)
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(cred))
}
