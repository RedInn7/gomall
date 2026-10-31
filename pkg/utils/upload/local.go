package upload

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	conf "github.com/RedInn7/gomall/config"
	util "github.com/RedInn7/gomall/pkg/utils/log"
)

const (
	maxImageSize = 5 * 1024 * 1024
	dirMode      = 0o755
	fileMode     = 0o644
)

var allowedImageTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// ProductUploadToLocalStatic 商品图存到 ProductPath 下，返回随机文件名。
func ProductUploadToLocalStatic(file multipart.File) (filePath string, err error) {
	return saveImage(file, "."+conf.Config.PhotoPath.ProductPath)
}

// AvatarUploadToLocalStatic 头像存到 AvatarPath 下，返回随机文件名。
// ponytail: userId / userName 已不参与命名，形参只为兼容 user/service.go 的现有调用，那边改调用时一起删。
func AvatarUploadToLocalStatic(file multipart.File, _ uint, _ string) (filePath string, err error) {
	return saveImage(file, "."+conf.Config.PhotoPath.AvatarPath)
}

// saveImage 校验图片后以随机 hex 命名落到 baseDir，返回文件名。
// 文件名不含用户 id、用户名或商品名，没法拿 /static 下的路径反推用户；
// 历史上传的 userN/、bossN/ 子目录文件不动，旧路径照常可访问。
func saveImage(file multipart.File, baseDir string) (string, error) {
	content, ext, err := readAndValidateImage(file)
	if err != nil {
		util.LogrusObj.Error(err)
		return "", err
	}

	if !DirExistOrNot(baseDir) {
		if !CreateDir(baseDir) {
			return "", errors.New("创建目录失败")
		}
	}

	var buf [16]byte
	_, _ = rand.Read(buf[:]) // Go 1.24 起 crypto/rand.Read 不会返回错误
	name := hex.EncodeToString(buf[:]) + ext
	if err := os.WriteFile(filepath.Join(baseDir, name), content, fileMode); err != nil {
		util.LogrusObj.Error(err)
		return "", err
	}

	return name, nil
}

func readAndValidateImage(file multipart.File) ([]byte, string, error) {
	limited := io.LimitReader(file, maxImageSize+1)
	content, err := io.ReadAll(limited)
	if err != nil {
		return nil, "", err
	}
	if len(content) == 0 {
		return nil, "", errors.New("文件内容为空")
	}
	if len(content) > maxImageSize {
		return nil, "", fmt.Errorf("文件大小超出限制 %d 字节", maxImageSize)
	}

	contentType := http.DetectContentType(content)
	ext, ok := allowedImageTypes[contentType]
	if !ok {
		return nil, "", fmt.Errorf("不支持的图片类型: %s", contentType)
	}
	return content, ext, nil
}

func DirExistOrNot(fileAddr string) bool {
	s, err := os.Stat(fileAddr)
	if err != nil {
		return false
	}
	return s.IsDir()
}

func CreateDir(dirName string) bool {
	if err := os.MkdirAll(dirName, dirMode); err != nil {
		util.LogrusObj.Error(err)
		return false
	}
	return true
}
