// Copyright 2014 Unknwon
//
// Licensed under the Apache License, Version 2.0 (the "License"): you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
// WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
// License for the specific language governing permissions and limitations
// under the License.

package webcore

import (
	"crypto/md5"
	"encoding/hex"
	"html/template"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"core/inject"
)

// Locale reprents a localization interface.
type Locale interface {
	Language() string
	Tr(string, ...interface{}) string
}

// RequestBody represents a request body.
type RequestBody struct {
	reader io.ReadCloser
}

// Bytes reads and returns content of request body in bytes.
func (rb *RequestBody) Bytes() ([]byte, error) {
	return ioutil.ReadAll(rb.reader)
}

// String reads and returns content of request body in string.
func (rb *RequestBody) String() (string, error) {
	data, err := rb.Bytes()
	return string(data), err
}

// ReadCloser returns a ReadCloser for request body.
func (rb *RequestBody) ReadCloser() io.ReadCloser {
	return rb.reader
}

// Request represents an HTTP request received by a server or to be sent by a client.
type Request struct {
	*http.Request
}

func (r *Request) Body() *RequestBody {
	return &RequestBody{r.Request.Body}
}

// Context represents the runtime context of current request of Macaron instance.
// It is the integration of most frequently used middlewares and helper methods.
type Context struct {
	inject.Injector
	handlers []Handler
	action   Handler
	index    int

	*Router
	Req    Request
	Resp   ResponseWriter
	params Params
	Render // Not nil only if you use macaran.Render middleware.
	Locale
	Data map[string]interface{}
}

func (c *Context) handler() Handler {
	if c.index < len(c.handlers) {
		return c.handlers[c.index]
	}
	if c.index == len(c.handlers) {
		return c.action
	}
	panic("invalid index for context handler")
}

func (c *Context) Next() {
	c.index += 1
	c.run()
}

func (c *Context) Written() bool {
	return c.Resp.Written()
}

func (c *Context) run() {
	for c.index <= len(c.handlers) {
		vals, err := c.Invoke(c.handler())
		if err != nil {
			panic(err)
		}
		c.index += 1

		// if the handler returned something, write it to the http response
		if len(vals) > 0 {
			ev := c.GetVal(reflect.TypeOf(ReturnHandler(nil)))
			handleReturn := ev.Interface().(ReturnHandler)
			handleReturn(c, vals)
		}

		if c.Written() {
			return
		}
	}
}

// RemoteAddr returns more real IP address.
func (ctx *Context) RemoteAddr() string {
	addr := ctx.Req.Header.Get("X-Real-IP")
	if len(addr) == 0 {
		addr = ctx.Req.Header.Get("X-Forwarded-For")
		if addr == "" {
			addr = ctx.Req.RemoteAddr
			if i := strings.LastIndex(addr, ":"); i > -1 {
				addr = addr[:i]
			}
		}
	}
	return addr
}

func (ctx *Context) renderHTML(status int, setName, tplName string, data ...interface{}) {
	if ctx.Render == nil {
		panic("renderer middleware hasn't been registered")
	}
	if len(data) <= 0 {
		ctx.Render.HTMLSet(status, setName, tplName, ctx.Data)
	} else if len(data) == 1 {
		ctx.Render.HTMLSet(status, setName, tplName, data[0])
	} else {
		ctx.Render.HTMLSet(status, setName, tplName, data[0], data[1].(HTMLOptions))
	}
}

// HTML calls Render.HTML but allows less arguments.
func (ctx *Context) HTML(status int, name string, data ...interface{}) {
	ctx.renderHTML(status, _DEFAULT_TPL_SET_NAME, name, data...)
}

func (ctx *Context) Echo(status int, body string) {
	ctx.Render.Echo(status, body)
}

func (ctx *Context) Txt(status int, filepath string) {
	ctx.Render.Txt(status, filepath)
}

func (ctx *Context) JS(status int, name string, binding interface{}) {
	ctx.Render.JS(status, name, binding)
}

// HTML calls Render.HTMLSet but allows less arguments.
func (ctx *Context) HTMLSet(status int, setName, tplName string, data ...interface{}) {
	ctx.renderHTML(status, setName, tplName, data...)
}

func (ctx *Context) Redirect(location string, status ...int) {
	code := http.StatusFound
	if len(status) == 1 {
		code = status[0]
	}

	http.Redirect(ctx.Resp, ctx.Req.Request, location, code)
}

// Query querys form parameter.
func (ctx *Context) Query(name string) string {
	if ctx.Req.Form == nil {
		ctx.Req.ParseForm()
	}
	return ctx.Req.Form.Get(name)
}

// QueryStrings returns a list of results by given query name.
func (ctx *Context) QueryStrings(name string) []string {
	if ctx.Req.Form == nil {
		ctx.Req.ParseForm()
	}

	vals, ok := ctx.Req.Form[name]
	if !ok {
		return []string{}
	}
	return vals
}

// QueryEscape returns escapred query result.
func (ctx *Context) QueryEscape(name string) string {
	return template.HTMLEscapeString(ctx.Query(name))
}

// QueryInt returns query result in int type.
func (ctx *Context) QueryInt(name string) int {
	return StrTo(ctx.Query(name)).MustInt()
}

// QueryInt64 returns query result in int64 type.
func (ctx *Context) QueryInt64(name string) int64 {
	return StrTo(ctx.Query(name)).MustInt64()
}

// QueryFloat64 returns query result in float64 type.
func (ctx *Context) QueryFloat64(name string) float64 {
	v, _ := strconv.ParseFloat(ctx.Query(name), 64)
	return v
}

//设置data的值
func (ctx *Context) Set(key string, val interface{}) {
	if ctx.Data == nil {
		ctx.Data = make(map[string]interface{})
	}
	ctx.Data[key] = val
}

// Params returns value of given param name.
// e.g. ctx.Params(":uid") or ctx.Params("uid")
func (ctx *Context) Params(name string) string {
	if len(name) == 0 {
		return ""
	}
	if name[0] != '*' && name[0] != ':' {
		name = ":" + name
	}
	return ctx.params[name]
}

// SetParams sets value of param with given name.
func (ctx *Context) SetParams(name, val string) {
	if !strings.HasPrefix(name, ":") {
		name = ":" + name
	}
	ctx.params[name] = val
}

// ParamsEscape returns escapred params result.
// e.g. ctx.ParamsEscape(":uname")
func (ctx *Context) ParamsEscape(name string) string {
	return template.HTMLEscapeString(ctx.Params(name))
}

// ParamsInt returns params result in int type.
// e.g. ctx.ParamsInt(":uid")
func (ctx *Context) ParamsInt(name string) int {
	return StrTo(ctx.Params(name)).MustInt()
}

// ParamsInt64 returns params result in int64 type.
// e.g. ctx.ParamsInt64(":uid")
func (ctx *Context) ParamsInt64(name string) int64 {
	return StrTo(ctx.Params(name)).MustInt64()
}

// ParamsFloat64 returns params result in int64 type.
// e.g. ctx.ParamsFloat64(":uid")
func (ctx *Context) ParamsFloat64(name string) float64 {
	v, _ := strconv.ParseFloat(ctx.Params(name), 64)
	return v
}

// GetFile returns information about user upload file by given form field name.
func (ctx *Context) GetFile(name string) (multipart.File, *multipart.FileHeader, error) {
	return ctx.Req.FormFile(name)
}

// SetCookie sets given cookie value to response header.
func (ctx *Context) SetCookie(name string, value string, others ...interface{}) {
	cookie := http.Cookie{}
	cookie.Name = name
	cookie.Value = url.QueryEscape(value)

	if len(others) > 0 {
		switch v := others[0].(type) {
		case int:
			cookie.MaxAge = v
		case int64:
			cookie.MaxAge = int(v)
		case int32:
			cookie.MaxAge = int(v)
		}
	}

	cookie.Path = "/"
	if len(others) > 1 {
		if v, ok := others[1].(string); ok && len(v) > 0 {
			cookie.Path = v
		}
	}

	if len(others) > 2 {
		if v, ok := others[2].(string); ok && len(v) > 0 {
			cookie.Domain = v
		}
	}

	if len(others) > 3 {
		switch v := others[3].(type) {
		case bool:
			cookie.Secure = v
		default:
			if others[3] != nil {
				cookie.Secure = true
			}
		}
	}

	if len(others) > 4 {
		if v, ok := others[4].(bool); ok && v {
			cookie.HttpOnly = true
		}
	}

	ctx.Resp.Header().Add("Set-Cookie", cookie.String())
}

// GetCookie returns given cookie value from request header.
func (ctx *Context) GetCookie(name string) string {
	cookie, err := ctx.Req.Cookie(name)
	if err != nil {
		return ""
	}
	val, _ := url.QueryUnescape(cookie.Value)
	return val
}

// GetCookieInt returns cookie result in int type.
func (ctx *Context) GetCookieInt(name string) int {
	return StrTo(ctx.GetCookie(name)).MustInt()
}

// GetCookieInt64 returns cookie result in int64 type.
func (ctx *Context) GetCookieInt64(name string) int64 {
	return StrTo(ctx.GetCookie(name)).MustInt64()
}

// GetCookieFloat64 returns cookie result in float64 type.
func (ctx *Context) GetCookieFloat64(name string) float64 {
	v, _ := strconv.ParseFloat(ctx.GetCookie(name), 64)
	return v
}

var defaultCookieSecret string

// SetDefaultCookieSecret sets global default secure cookie secret.
func (m *Macaron) SetDefaultCookieSecret(secret string) {
	defaultCookieSecret = secret
}

// SetSecureCookie sets given cookie value to response header with default secret string.
func (ctx *Context) SetSecureCookie(name, value string, others ...interface{}) {
	ctx.SetSuperSecureCookie(defaultCookieSecret, name, value, others...)
}

// GetSecureCookie returns given cookie value from request header with default secret string.
func (ctx *Context) GetSecureCookie(key string) (string, bool) {
	return ctx.GetSuperSecureCookie(defaultCookieSecret, key)
}

// SetSuperSecureCookie sets given cookie value to response header with secret string.
func (ctx *Context) SetSuperSecureCookie(secret, name, value string, others ...interface{}) {
	m := md5.Sum([]byte(secret))
	secret = hex.EncodeToString(m[:])
	text, err := AESEncrypt([]byte(secret), []byte(value))
	if err != nil {
		panic("error encrypting cookie: " + err.Error())
	}
	ctx.SetCookie(name, hex.EncodeToString(text), others...)
}

// GetSuperSecureCookie returns given cookie value from request header with secret string.
func (ctx *Context) GetSuperSecureCookie(secret, key string) (string, bool) {
	val := ctx.GetCookie(key)
	if val == "" {
		return "", false
	}

	data, err := hex.DecodeString(val)
	if err != nil {
		return "", false
	}

	m := md5.Sum([]byte(secret))
	secret = hex.EncodeToString(m[:])
	text, err := AESDecrypt([]byte(secret), data)
	return string(text), err == nil
}

// ServeContent serves given content to response.
func (ctx *Context) ServeContent(name string, r io.ReadSeeker, params ...interface{}) {
	modtime := time.Now()
	for _, p := range params {
		switch v := p.(type) {
		case time.Time:
			modtime = v
		}
	}
	ctx.Resp.Header().Set("Content-Description", "Raw content")
	ctx.Resp.Header().Set("Content-Type", "text/plain")
	ctx.Resp.Header().Set("Expires", "0")
	ctx.Resp.Header().Set("Cache-Control", "must-revalidate")
	ctx.Resp.Header().Set("Pragma", "public")
	http.ServeContent(ctx.Resp, ctx.Req.Request, name, modtime, r)
}

// ServeFile serves given file to response.
func (ctx *Context) ServeFile(file string, names ...string) {
	var name string
	if len(names) > 0 {
		name = names[0]
	} else {
		name = path.Base(file)
	}
	ctx.Resp.Header().Set("Content-Description", "File Transfer")
	ctx.Resp.Header().Set("Content-Type", "application/octet-stream")
	ctx.Resp.Header().Set("Content-Disposition", "attachment; filename="+name)
	ctx.Resp.Header().Set("Content-Transfer-Encoding", "binary")
	ctx.Resp.Header().Set("Expires", "0")
	ctx.Resp.Header().Set("Cache-Control", "must-revalidate")
	ctx.Resp.Header().Set("Pragma", "public")
	http.ServeFile(ctx.Resp, ctx.Req.Request, file)
}

// ChangeStaticPath changes static path from old to new one.
func (ctx *Context) ChangeStaticPath(oldPath, newPath string) {
	if !filepath.IsAbs(oldPath) {
		oldPath = filepath.Join(Root, oldPath)
	}
	dir := statics.Get(oldPath)
	if dir != nil {
		statics.Delete(oldPath)

		if !filepath.IsAbs(newPath) {
			newPath = filepath.Join(Root, newPath)
		}
		*dir = http.Dir(newPath)
		statics.Set(dir)
	}
}
