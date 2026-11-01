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

// TimeOf 解析 GenSnowflakeID 生成的 ID（十进制字符串）里的生成时刻。
func TimeOf(id string) (time.Time, error) {
	sid, err := snowflake.ParseString(id)
	if err != nil {
		return time.Time{}, err
	}
	return time.UnixMilli(sid.Time()), nil
}
