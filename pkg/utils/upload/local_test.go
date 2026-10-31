package upload

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/sirupsen/logrus"

	util "github.com/RedInn7/gomall/pkg/utils/log"
)

// memFile 用内存字节实现 multipart.File。
type memFile struct{ *bytes.Reader }

func (memFile) Close() error { return nil }

func newMemFile(b []byte) memFile { return memFile{bytes.NewReader(b)} }

var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)

func TestSaveImageUsesRandomName(t *testing.T) {
	prev := util.LogrusObj
	util.LogrusObj = logrus.New()
	util.LogrusObj.Out = io.Discard
	t.Cleanup(func() { util.LogrusObj = prev })
	dir := t.TempDir()

	first, err := saveImage(newMemFile(pngBytes), dir)
	if err != nil {
		t.Fatalf("save png: %v", err)
	}
	second, err := saveImage(newMemFile(pngBytes), dir)
	if err != nil {
		t.Fatalf("save png: %v", err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}\.png$`).MatchString(first) || first == second {
		t.Fatalf("文件名应为随机 hex 且互不相同: %q %q", first, second)
	}
	if _, err := os.Stat(filepath.Join(dir, first)); err != nil {
		t.Fatalf("file not written: %v", err)
	}

	if _, err := saveImage(newMemFile([]byte("<html>not an image</html>")), dir); err == nil {
		t.Fatal("非图片内容应被拒绝")
	}
}

// OSS 上传在连七牛之前就要拦下非图片与超大文件（不配置 OSS 也能跑）。
func TestUploadToQiNiuValidatesBeforeUpload(t *testing.T) {
	for name, content := range map[string][]byte{
		"非图片":    []byte("<script>alert(1)</script>"),
		"超过 5MB": append(append([]byte{}, pngBytes...), make([]byte, maxImageSize)...),
	} {
		if _, err := UploadToQiNiu(newMemFile(content), int64(len(content))); err == nil {
			t.Fatalf("%s 应被拒绝", name)
		}
	}
}
