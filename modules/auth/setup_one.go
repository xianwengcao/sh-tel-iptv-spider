package auth

import (
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"github.com/robertkrimen/otto"
	"iptv-spider-sh/global"
	"iptv-spider-sh/model"
	"iptv-spider-sh/utils"
	"strings"
)

func (c *Client) pre4kLogAuth() *goquery.Document {
	u := fmt.Sprintf("http://%s/iptv3a/4kLogAuth.do", c.pre4kLogAuthAddr)
	resp := c.httpClient.Request(u, "get", map[string]string{
		"Action":     "Login",
		"UserID":     c.userID,
		"SN":         c.sn,
		"Type":       "iptv4k",
		"Mode":       "MENU.SMG-4K",
		"FCCSupport": "1",
	})
	return utils.CreateHtmlDocByBytes(u, resp.GetRespBytes())
}

func (c *Client) r4kLogAuth(doc *goquery.Document) *goquery.Document {
	uri, method, formMap := utils.GetFromParamByHtml(doc)
	if strings.TrimSpace(uri) == "" {
		// 上一步没有拿到可解析的响应时，这里以前会带着空 URL 再发一次请求，
		// 报出 `Get "": unsupported protocol scheme ""`，把真正的失败原因
		// （通常是 pre4kLogAuth 网络超时）整个盖掉。这里直接跳过并说明原因。
		global.LOG.Error("4kLogAuth 第二步跳过：上一步响应里没有解析到表单 action，" +
			"通常是 pre4kLogAuth 请求失败（网络不通 / 认证服务器地址不对）")
		return doc
	}
	resp := c.httpClient.Request(uri, method, formMap)
	respDoc := utils.CreateHtmlDocByBytes(uri, resp.GetRespBytes())
	if respDoc != nil {
		c.R4kLoginHost = respDoc.Url.Host
	}
	return respDoc
}

func (c *Client) ottAuth(doc *goquery.Document) *goquery.Document {
	c.jsVM.RunScriptForHtml(doc)
	encryptToken := c.jsVM.GetString("encrytoken")
	AuthModel := model.NewAuthenticator(encryptToken, c.userID, c.sn, c.ip, c.macAddr)

	uri, method, formMap := utils.GetFromParamByHtml(doc)
	if strings.TrimSpace(uri) == "" {
		global.LOG.Error("ottAuth 跳过：上一步响应里没有解析到表单 action，认证流程无法继续")
		return doc
	}
	formMap["authenticator"] = AuthModel.GetEncryptString()
	resp := c.httpClient.Request(uri, method, formMap)
	return utils.CreateHtmlDocByBytes(uri, resp.GetRespBytes())
}

func (c *Client) processChannel(doc *goquery.Document) []model.Channel {
	var channels []model.Channel
	c.jsVM.Reset()
	c.jsVM.RunScriptForHtml(doc)

	c.jsVM.Set("AddChannel", func(call otto.FunctionCall) otto.Value {
		channelStr := call.Argument(0).String()
		channels = append(channels, GetChannelFormString(channelStr))
		return otto.Value{}
	})
	c.jsVM.RunScript(`
		for (var i = 0; i < channelArray.length; i++) {
			AddChannel(channelArray[i]);
		}
	`)
	return channels
}
