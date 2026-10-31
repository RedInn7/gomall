package upload

import (
	"bytes"
	"context"
	"mime/multipart"

	"github.com/qiniu/go-sdk/v7/auth/qbox"
	"github.com/qiniu/go-sdk/v7/storage"

	conf "github.com/RedInn7/gomall/config"
)

// UploadToQiNiu 封装上传图片到七牛云然后返回状态和图片的url，单张。
// 上传前与本地存储走同一套校验（魔数嗅探 + 5MB 上限），并显式指定 MimeType，不让七牛自行猜测。
// ponytail: 大小以实际读到的字节为准，第二个形参只为兼容 user/service.go 的现有调用，那边改调用时一起删。
func UploadToQiNiu(file multipart.File, _ int64) (path string, err error) {
	content, mimeType, err := readAndValidateImage(file)
	if err != nil {
		return "", err
	}
	qConfig := conf.Config.Oss
	var AccessKey = qConfig.AccessKeyId
	var SerectKey = qConfig.AccessKeySecret
	var Bucket = qConfig.BucketName
	var ImgUrl = qConfig.QiNiuServer
	putPlicy := storage.PutPolicy{
		Scope: Bucket,
	}
	mac := qbox.NewMac(AccessKey, SerectKey)
	upToken := putPlicy.UploadToken(mac)
	cfg := storage.Config{
		Zone:          &storage.ZoneHuanan,
		UseCdnDomains: false,
		UseHTTPS:      false,
	}
	putExtra := storage.PutExtra{MimeType: mimeType}
	formUploader := storage.NewFormUploader(&cfg)
	ret := storage.PutRet{}
	err = formUploader.PutWithoutKey(context.Background(), &ret, upToken, bytes.NewReader(content), int64(len(content)), &putExtra)
	if err != nil {
		return "", err
	}
	url := ImgUrl + ret.Key
	return url, nil
}
