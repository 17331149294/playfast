package node

import (
	"PlayFast/internal/api"
	"PlayFast/internal/http-client"
	"encoding/json"
	"fmt"
)

type Proxy struct {
	Name     string `json:"name"`
	Method   string `json:"method"`
	Password string `json:"password"`
	Host     string `json:"host"`
	Port     uint16 `json:"port"`
	Protocol string `json:"protocol"`
}

func Get() []Proxy {
	data := make([]Proxy, 0)
	all, err := http_client.GET(fmt.Sprintf("%s/proxy.json", api.GetApiDomain()))
	if err != nil {
		return data
	}
	_ = json.Unmarshal(all, &data)
	return data
}
