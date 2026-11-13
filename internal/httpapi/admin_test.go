package httpapi

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func (c *client) upload(t *testing.T, path, filename string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", filename)
	_, _ = fw.Write(content)
	_ = mw.Close()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, &buf)
	req.RemoteAddr = "192.168.1.9:50000"
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", origin)
	req.Header.Set(csrfHeader, c.csrf)
	req.AddCookie(c.cookie)
	w := httptest.NewRecorder()
	c.e.api.ServeHTTP(w, req)
	return w
}

func TestAttachments(t *testing.T) {
	e, _ := fullEnv(t)
	c := &client{e: e}
	c.login(t)
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
	w := c.upload(t, basePath+"/projects/libfoo/attachments", "../shot.png", png)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload %d %s", w.Code, w.Body)
	}
	img := decodeBody[attachmentJSON](t, w.Body.Bytes())
	if !img.Inline || img.Filename != "shot.png" {
		t.Fatalf("attachment %+v", img)
	}
	dl := c.do(t, http.MethodGet, basePath+"/attachments/"+img.ID, nil, nil)
	if dl.Code != http.StatusOK || dl.Header().Get("Content-Type") != "image/png" || !strings.HasPrefix(dl.Header().Get("Content-Disposition"), "inline") {
		t.Fatalf("download %d %v", dl.Code, dl.Header())
	}
	if !strings.Contains(dl.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("download CSP %q", dl.Header().Get("Content-Security-Policy"))
	}
	w = c.upload(t, basePath+"/projects/libfoo/attachments", "x.html", []byte("<html><script>alert(1)</script>"))
	html := decodeBody[attachmentJSON](t, w.Body.Bytes())
	dl = c.do(t, http.MethodGet, basePath+"/attachments/"+html.ID, nil, nil)
	if dl.Header().Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(dl.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("HTML served as %v", dl.Header())
	}
	body, _ := io.ReadAll(dl.Body)
	if !bytes.Contains(body, []byte("<script>")) {
		t.Fatal("download body changed")
	}
	meta := c.do(t, http.MethodGet, basePath+"/attachments/"+img.ID+"/meta", nil, nil)
	if meta.Code != http.StatusOK || !strings.HasPrefix(meta.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("meta %d %v", meta.Code, meta.Header())
	}
	if got := decodeBody[attachmentJSON](t, meta.Body.Bytes()); got != img {
		t.Fatalf("meta %+v, want %+v", got, img)
	}
	if w := c.do(t, http.MethodDelete, basePath+"/attachments/"+html.ID, nil, nil); w.Code != http.StatusNoContent {
		t.Fatalf("delete %d", w.Code)
	}
	if w := c.do(t, http.MethodGet, basePath+"/attachments/"+html.ID+"/meta", nil, nil); w.Code != http.StatusNotFound {
		t.Fatalf("meta of a deleted attachment %d", w.Code)
	}
}

func TestInvitationsAndUsers(t *testing.T) {
	e, _ := fullEnv(t)
	c := &client{e: e}
	c.login(t)
	w := c.do(t, http.MethodPost, basePath+"/admin/invitations", map[string]string{"role": "member"}, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("invite %d %s", w.Code, w.Body)
	}
	out := decodeBody[struct {
		Link string `json:"link"`
		Code string `json:"code"`
	}](t, w.Body.Bytes())
	if out.Link != "https://review.local:8444/enroll#ca="+strings.Repeat("ab", 32) || strings.Contains(out.Link, out.Code) || strings.Count(out.Code, "-") != 5 {
		t.Fatalf("link %q code %q", out.Link, out.Code)
	}
	if w := c.do(t, http.MethodGet, basePath+"/admin/invitations", nil, nil); strings.Contains(w.Body.String(), out.Code) {
		t.Fatal("invitation list shows the code")
	}
	if w := c.do(t, http.MethodGet, basePath+"/users", nil, nil); !strings.Contains(w.Body.String(), `"username":"root"`) {
		t.Fatalf("users %s", w.Body)
	}
	if w := c.do(t, http.MethodGet, basePath+"/inbox?unread=true", nil, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"notifications":[]`) {
		t.Fatalf("inbox %d %s", w.Code, w.Body)
	}
}

func TestExportAndPurge(t *testing.T) {
	e, _ := fullEnv(t)
	c := &client{e: e}
	c.login(t)
	if w := c.do(t, http.MethodPost, basePath+"/admin/projects/libfoo/purge", map[string]string{"confirm_name": "libfoo"}, nil); w.Code != http.StatusConflict || errorCode(t, w) != codeExportRequired {
		t.Fatalf("purge before export %d %s", w.Code, w.Body)
	}
	exp := c.do(t, http.MethodPost, basePath+"/admin/projects/libfoo/export", nil, nil)
	if exp.Code != http.StatusOK || exp.Header().Get("Content-Type") != "application/zip" || exp.Body.Len() == 0 {
		t.Fatalf("export %d %v", exp.Code, exp.Header())
	}
	if sum := exp.Result().Trailer.Get("X-Garret-Export-SHA256"); len(sum) != 64 {
		t.Fatalf("export digest trailer %q", sum)
	}
	if w := c.do(t, http.MethodPost, basePath+"/admin/projects/libfoo/purge", map[string]string{"confirm_name": "wrong"}, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("mistyped name %d", w.Code)
	}
	if w := c.do(t, http.MethodPost, basePath+"/admin/projects/libfoo/purge", map[string]string{"confirm_name": "libfoo"}, nil); w.Code != http.StatusNoContent {
		t.Fatalf("purge %d %s", w.Code, w.Body)
	}
	if w := c.do(t, http.MethodGet, basePath+"/projects/libfoo", nil, nil); w.Code != http.StatusNotFound {
		t.Fatalf("project after purge %d", w.Code)
	}
}
