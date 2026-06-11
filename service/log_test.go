package service

import (
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
