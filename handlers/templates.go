package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

var pageTemplates = make(map[string]*template.Template)

// templateFuncs — функции, доступные во всех шаблонах.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"atoi": func(s string) int {
			i, _ := strconv.Atoi(s)
			return i
		},
		// asset возвращает URL статики с версией для сброса кэша
		"asset": AssetURL,
	}
}

// LoadTemplates загружает layout и все страницы один раз при старте.
func LoadTemplates(dir string) error {
	layoutPath := filepath.Join(dir, "layout.html")

	if _, err := os.Stat(layoutPath); err != nil {
		return fmt.Errorf("layout.html не найден: %w", err)
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.html"))
	if err != nil {
		return err
	}

	if len(files) == 0 {
		return errors.New("шаблоны не найдены")
	}

	newTemplates := make(map[string]*template.Template)
	loaded := 0

	for _, file := range files {
		base := filepath.Base(file)

		if base == "layout.html" {
			continue
		}

		t := template.New(base).Funcs(templateFuncs())
		t, err = t.ParseFiles(layoutPath, file)
		if err != nil {
			return fmt.Errorf("ошибка парсинга шаблона %s: %w", base, err)
		}

		if t.Lookup("layout") == nil {
			return fmt.Errorf("в шаблоне %s не найден блок layout", base)
		}

		if t.Lookup("content") == nil {
			return fmt.Errorf("в шаблоне %s не найден блок content", base)
		}

		newTemplates[base] = t
		loaded++
	}

	if loaded == 0 {
		return errors.New("не найдены страницы шаблонов")
	}

	pageTemplates = newTemplates
	log.Printf("✅ Загружено шаблонов: %d", loaded)
	return nil
}

// renderTemplate выполняет страницу через общий шаблон layout.
func renderTemplate(w http.ResponseWriter, name string, data interface{}) {
	t, ok := pageTemplates[name]
	if !ok {
		log.Printf("❌ Шаблон не найден: %s", name)
		http.Error(w, "Шаблон не найден", http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer

	if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
		log.Printf("❌ Ошибка рендера шаблона %s: %v", name, err)
		http.Error(w, "Ошибка отображения страницы", http.StatusInternalServerError)
		return
	}

	// HTML не кэшируется, чтобы браузер всегда видел свежую ссылку на CSS
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	if _, err := buf.WriteTo(w); err != nil {
		log.Printf("⚠️ Не удалось отправить ответ для шаблона %s: %v", name, err)
	}
}
