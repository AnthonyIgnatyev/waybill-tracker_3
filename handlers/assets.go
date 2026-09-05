package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
)

// assetVersion — короткий хеш содержимого папки static.
var assetVersion = "1"

// InitAssetVersion считает хеш всех файлов в dir и сохранит его как версию.
func InitAssetVersion(dir string) {
	h := sha256.New()

	var files []string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			files = append(files, path)
		}
		return nil
	})

	if len(files) == 0 {
		log.Printf("⚠️ InitAssetVersion: папка %s пуста", dir)
		return
	}

	sort.Strings(files)

	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		_, _ = io.Copy(h, fh)
		_ = fh.Close()
	}

	assetVersion = hex.EncodeToString(h.Sum(nil))[:10]
	log.Println("✅ Версия статики:", assetVersion)
}

// AssetURL возвращает URL статического файла с версией для сброса кэша.
func AssetURL(name string) string {
	return "/static/" + name + "?v=" + assetVersion
}
