package templates

import (
	"embed"
	"eth2-exporter/utils"
	"fmt"
	"html/template"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
)

var logger = logrus.New().WithField("module", "templates")

var (
	//go:embed *
	Files embed.FS
)

var templateCache = make(map[string]*template.Template)
var templateCacheMux = &sync.RWMutex{}
var templateFuncs = utils.GetTemplateFuncs()

// compile time check for templates
var _ error = CompileTimeCheck(fs.FS(Files))

func GetTemplate(files ...string) *template.Template {
	name := strings.Join(files, "-")
	if utils.Config.Frontend.Debug {
		for i := range files {
			files[i] = "templates/" + files[i]
		}
		return template.Must(template.New(name).Funcs(template.FuncMap(templateFuncs)).ParseFiles(files...))
	}

	templateCacheMux.RLock()
	if templateCache[name] != nil {
		templateCacheMux.RUnlock()
		return templateCache[name]
	}
	templateCacheMux.RUnlock()

	tmpl := template.Must(template.New(name).Funcs(template.FuncMap(templateFuncs)).ParseFS(Files, files...))
	templateCacheMux.Lock()
	templateCache[name] = tmpl
	templateCacheMux.Unlock()

	return templateCache[name]
}

func CompileTimeCheck(fsys fs.FS) error {
	files, err := getFileSysNames(fsys, ".")
	if err != nil {
		return err
	}
	template.Must(template.New("layout").Funcs(template.FuncMap(templateFuncs)).ParseFS(Files, files...))
	logger.Infof("compile time check completed")

	return nil
}

func getFileSysNames(fsys fs.FS, dirname string) ([]string, error) {
	entry, err := fs.ReadDir(fsys, dirname)
	if err != nil {
		return nil, fmt.Errorf("error reading embed directory, err: %w", err)
	}

	files := make([]string, 0, 100)
	for _, f := range entry {
		info, err := f.Info()
		if err != nil {
			return nil, fmt.Errorf("error returning file info err: %w", err)
		}
		if !f.IsDir() {
			files = append(files, filepath.Join(dirname, info.Name()))
		} else {
			names, err := getFileSysNames(fsys, info.Name())
			if err != nil {
				return nil, err
			}
			files = append(files, names...)
		}
	}

	return files, nil
}
