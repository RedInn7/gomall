package snowflake

import (
	"github.com/bwmarrin/snowflake"
	"time"
)

var node *snowflake.Node

func InitSnowflake(machineID int64) {
	snowflake.Epoch = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano() / 1000000
	var err error
	node, err = snowflake.NewNode(machineID)
	if err != nil {
		panic(err)
	}
}

func GenSnowflakeID() int64 {
	return node.Generate().Int64()
}

func GenSnowflakeIDStr() string {
	return node.Generate().String()
}
