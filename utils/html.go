package utils

import (
	"bytes"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"iptv-spider-sh/global"
	"net/url"
	"strings"
)

// CreateHtmlDocByBytes 把 HTTP 响应字节解析成 goquery 文档。
//
// 约定：永远返回非 nil 文档（解析失败时回退为空文档），
// 避免调用方在 doc.Url / doc.Find 上出现 panic。
func CreateHtmlDocByBytes(uri string, resp []byte) *goquery.Document {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(resp))
	if err != nil || doc == nil {
		global.LOG.Error(fmt.Sprintf("解析响应内容失败(uri=%s, %d 字节): %v", uri, len(resp), err))
		// 兜底：解析空内容得到一个空文档
		doc, err = goquery.NewDocumentFromReader(strings.NewReader("<html><body></body></html>"))
		if err != nil || doc == nil {
			return doc
		}
	}
	doc.Url, _ = url.Parse(uri)
	return doc
}

func GetFromParamByHtml(d *goquery.Document, sec ...string) (uri, method string, formMap map[string]string) {
	if d == nil {
		return
	}
	s := "form"
	if len(sec) == 1 {
		s = sec[0]
	}
	nodes := d.Find(s)
	// 处理Form表单
	if len(nodes.Nodes) != 1 {
		return
	}
	form := nodes.First()
	uri = form.AttrOr("action", "")
	if !strings.HasPrefix(uri, "http") {
		u := fmt.Sprintf("%s://%s", d.Url.Scheme, d.Url.Host)
		if strings.HasPrefix(uri, "/") {
			u += uri
		} else {
			i := strings.LastIndex(d.Url.Path, "/")
			if i > 0 {
				u = fmt.Sprintf("%s%s/%s", u, d.Url.Path[:i], uri)
			}
		}
		uri = u
	}
	method = form.AttrOr("method", "get")
	child := form.Children()
	formMap = map[string]string{}
	child.Each(func(_ int, s *goquery.Selection) {
		k := s.AttrOr("name", "")
		v := s.AttrOr("value", "")
		formMap[k] = v
	})
	return
}

func GetScriptsFormHtml(d *goquery.Document) []string {
	var scriptsArr []string
	scripts := d.Find("script")
	scripts.Each(func(_ int, s *goquery.Selection) {
		c := s.Nodes[0].FirstChild
		if c == nil {
			return
		}
		scriptsArr = append(scriptsArr, c.Data)
	})
	return scriptsArr
}
