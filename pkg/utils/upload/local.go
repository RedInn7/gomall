package upload

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

func ProductUploadToLocalStatic(file multipart.File, bossId uint, productName string) (filePath string, err error) {
	bId := strconv.Itoa(int(bossId))
	basePath := "." + conf.Config.PhotoPath.ProductPath + "boss" + bId + "/"
	return saveImage(file, basePath, productName, fmt.Sprintf("boss%s/", bId))
}

func AvatarUploadToLocalStatic(file multipart.File, userId uint, userName string) (filePath string, err error) {
	uId := strconv.Itoa(int(userId))
	basePath := "." + conf.Config.PhotoPath.AvatarPath + "user" + uId + "/"
	return saveImage(file, basePath, userName, fmt.Sprintf("user%s/", uId))
}

func saveImage(file multipart.File, baseDir, name, returnPrefix string) (string, error) {
	safe := safeName(name)
	if safe == "" {
		return "", errors.New("文件名非法")
	}

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

	fullPath := filepath.Join(baseDir, safe+ext)
	if err := os.WriteFile(fullPath, content, fileMode); err != nil {
		util.LogrusObj.Error(err)
		return "", err
	}

	return returnPrefix + safe + ext, nil
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

func safeName(name string) string {
	name = strings.TrimSpace(name)
	name = filepath.Base(name)
	if name == "." || name == ".." || name == "/" {
		return ""
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
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
