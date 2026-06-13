package es

import (
	"fmt"
	"log"

	elastic "github.com/elastic/go-elasticsearch"

	conf "github.com/RedInn7/gomall/config"
)

var EsClient *elastic.Client

// InitEs 初始化es
func InitEs() {
	eConfig := conf.Config.Es
	esConn := fmt.Sprintf("http://%s:%s", eConfig.EsHost, eConfig.EsPort)
	cfg := elastic.Config{
		Addresses: []string{esConn},
	}
	client, err := elastic.NewClient(cfg)
	if err != nil {
		log.Panic(err)
	}
	EsClient = client
}
