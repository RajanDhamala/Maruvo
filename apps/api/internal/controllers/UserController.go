package controller

import (
	"encoding/json"
	"net/http"
)

func (c *Controller) Test(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]string{
		"message": "user id found in cookie",
	})
}
