package service

import (
	"errors"
	"io"

	"github.com/sirupsen/logrus"

	logpkg "github.com/RedInn7/gomall/pkg/utils/log"
)

func initLogForTest() {
	if logpkg.LogrusObj == nil {
		l := logrus.New()
		l.Out = io.Discard
		logpkg.LogrusObj = l
	}
}

// safeCall 兜住 service 在 DB 未初始化时的 nil-pointer panic，让测试以 "err != nil" 形式收尾。
func safeCall(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("recovered panic")
		}
	}()
	return fn()
}
