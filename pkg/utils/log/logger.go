package log

import (
	"fmt"
	"log"
	"os"
	"path"
	"time"

	"github.com/sirupsen/logrus"
)

var LogrusObj *logrus.Logger

func InitLog() {
	if LogrusObj != nil {
		src, _ := setOutputFile()
		// 设置输出
		LogrusObj.Out = src
		return
	}
	// 实例化
	logger := logrus.New()
	src, _ := setOutputFile()
	// 设置输出
	logger.Out = src
	// 设置日志级别
	logger.SetLevel(logrus.DebugLevel)
	// 设置日志格式
	logger.SetFormatter(&logrus.JSONFormatter{
		TimestampFormat: "2006-01-02 15:04:05",
	})
	logger.AddHook(stderrHook{})
	LogrusObj = logger
}

// stderrHook 把 Fatal/Panic 日志同时打到终端。日志只写文件，
// 没有这个 hook 时进程退出了终端也看不到原因。
type stderrHook struct{}

func (stderrHook) Levels() []logrus.Level {
	return []logrus.Level{logrus.PanicLevel, logrus.FatalLevel}
}

func (stderrHook) Fire(e *logrus.Entry) error {
	_, err := fmt.Fprintf(os.Stderr, "%s: %s\n", e.Level, e.Message)
	return err
}

func setOutputFile() (*os.File, error) {
	now := time.Now()
	logFilePath := ""
	if dir, err := os.Getwd(); err == nil {
		logFilePath = dir + "/logs/"
	}
	_, err := os.Stat(logFilePath)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(logFilePath, 0777); err != nil {
			log.Println(err.Error())
			return nil, err
		}
	}
	logFileName := now.Format("2006-01-02") + ".log"
	// 日志文件
	fileName := path.Join(logFilePath, logFileName)
	if _, err := os.Stat(fileName); err != nil {
		if _, err := os.Create(fileName); err != nil {
			log.Println(err.Error())
			return nil, err
		}
	}
	// 写入文件
	src, err := os.OpenFile(fileName, os.O_APPEND|os.O_WRONLY, os.ModeAppend)
	if err != nil {
		log.Println(err)
		return nil, err
	}
	return src, nil
}
