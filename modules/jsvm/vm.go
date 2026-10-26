package jsvm

import (
	"github.com/PuerkitoBio/goquery"
	"github.com/robertkrimen/otto"
)

type VM struct {
	jsVM      *otto.Otto
	resetJsVM bool
}

func (v *VM) Reset() {
	v.resetJsVM = true
}

func (v *VM) RunScript(s string) otto.Value {
	v.createVM()
	value, err := v.jsVM.Run(s)
	if err != nil {
		return otto.Value{}
	}
	return value
}

func (v *VM) RunScriptForHtml(doc *goquery.Document) *VM {
	v.createVM()
	if doc == nil {
		// 上游响应为空时 CreateHtmlDocByBytes 可能拿不到文档；这里直接返回，
		// 让调用方去做错误处理，而不是在 doc.Find 上 panic。
		return v
	}
	scripts := doc.Find("script")
	scripts.Each(func(_ int, s *goquery.Selection) {
		c := s.Nodes[0].FirstChild
		if c == nil {
			return
		}
		v.jsVM.Run(c.Data)
	})
	return v
}

func (v *VM) GetVM() *otto.Otto {
	v.createVM()
	return v.jsVM
}

func (v *VM) Get(name string) (otto.Value, error) {
	return v.jsVM.Get(name)
}

func (v *VM) GetString(name string) string {
	value, err := v.Get(name)
	if err != nil {
		return ""
	}
	return value.String()
}

func (v *VM) Set(name string, value interface{}) error {
	return v.jsVM.Set(name, value)
}

func (v *VM) createVM() {
	if v.jsVM == nil || v.resetJsVM {
		v.resetJsVM = false
		v.jsVM = otto.New()
	}
}

func New() *VM {
	return &VM{
		jsVM:      otto.New(),
		resetJsVM: false,
	}
}
