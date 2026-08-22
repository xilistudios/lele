package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/mymmrac/telego"
)

func main() {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"x","username":"b"}}`)
	}))
	bot, err := telego.NewBot("123456789:AAAbbbbCCCCddddEEEEffffgggghhhhiiiijjjj",
		telego.WithAPIServer(s.URL),
		telego.WithDiscardLogger(),
	)
	fmt.Printf("bot=%v err=%v\n", bot, err)
	s.Close()
}
