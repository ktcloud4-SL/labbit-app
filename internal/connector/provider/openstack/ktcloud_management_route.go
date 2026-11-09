package openstackprovider

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/netip"
	"net/textproto"
	"strings"
)

//go:embed ktcloud_management_route.py
var ktManagementRoutePython string

//go:embed ktcloud_management_route_verify.py
var ktManagementRouteVerifyPython string

func ktManagementRouteScript(config ProvisionConfig, verify bool) ([]byte, error) {
	management, me := netip.ParsePrefix(config.KTCloudManagementCIDR)
	source, se := netip.ParsePrefix(config.SSHAllowedCIDR)
	if me != nil || se != nil || !management.Addr().Is4() || management.Bits() != 24 || management != management.Masked() || !source.Addr().Is4() || source.Bits() != 32 || management.Contains(source.Addr()) {
		return nil, ErrProvisionConfig
	}
	data, _ := json.Marshal(map[string]string{"management": management.String(), "source": source.Addr().String()})
	mode := "False"
	body := ktManagementRoutePython
	invoke := "prepare()"
	if verify {
		mode = "True"
		body += ktManagementRouteVerifyPython
		invoke = "verify(*prepare())"
	}
	return []byte("#!/usr/bin/python3\nC=" + string(data) + "\nVERIFY=" + mode + "\n" + body + "\ntry:\n    " + invoke + "\nexcept Exception as error:\n    raise SystemExit(failure(error)) from None\n"), nil
}

// Compose a separate first-boot part. The user's immutable content remains a
// separate part, rather than being prefixed with infrastructure commands.
// This exact deterministic composition is checked before any Create/Reset.
func ktManagementUserData(config ProvisionConfig, original []byte) ([]byte, error) {
	helper, err := ktManagementRouteScript(config, false)
	if err != nil {
		return nil, err
	}
	var body bytes.Buffer
	boundary := "labbit-kt-management-route"
	for bytes.Contains(original, []byte(boundary)) {
		boundary += "x"
	}
	if err := multipart.NewWriter(io.Discard).SetBoundary(boundary); err != nil {
		return nil, ErrInvalidResourceSpec
	}
	body.WriteString("MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n\r\n")
	body.WriteString("--" + boundary + "\r\nContent-Type: text/cloud-boothook\r\nContent-Disposition: attachment; filename=labbit-management-route\r\n\r\n")
	body.Write(helper)
	if len(original) != 0 {
		// cloud-init recognizes an existing MIME archive by this header. Preserve
		// its hierarchy/part types; plain content uses cloud-init header detection.
		prefix := original[:min(len(original), 4096)]
		if bytes.Contains(bytes.ToLower(prefix), []byte("mime-version:")) {
			message, parseErr := mail.ReadMessage(bytes.NewReader(original))
			if parseErr != nil {
				return nil, ErrInvalidResourceSpec
			}
			mediaType, _, parseErr := mime.ParseMediaType(message.Header.Get("Content-Type"))
			if parseErr != nil || !strings.HasPrefix(mediaType, "multipart/") || validateKTMultipart(textproto.MIMEHeader(message.Header), message.Body, 0) != nil {
				return nil, ErrInvalidResourceSpec
			}
			// Keep even the original header folding/case and line endings intact.
			body.WriteString("\r\n--" + boundary + "\r\n")
			body.Write(original)
		} else {
			body.WriteString("\r\n--" + boundary + "\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\nContent-Disposition: attachment; filename=labbit-user-startup\r\n\r\n")
			body.WriteString(base64.StdEncoding.EncodeToString(original))
		}
	}
	body.WriteString("\r\n--" + boundary + "--\r\n")
	if _, err := ktCloudCompressedUserData(body.Bytes()); err != nil {
		return nil, fmt.Errorf("%w: management bootstrap transport limit", ErrInvalidResourceSpec)
	}
	return body.Bytes(), nil
}

// Read every multipart boundary before accepting immutable user content. The
// cloud-init MIME walker otherwise silently drops malformed nested archives.
func validateKTMultipart(header textproto.MIMEHeader, body io.Reader, depth int) error {
	if depth > 16 {
		return ErrInvalidResourceSpec
	}
	contentType := header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain"
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ErrInvalidResourceSpec
	}
	if !strings.HasPrefix(mediaType, "multipart/") {
		_, err = io.Copy(io.Discard, body)
		return err
	}
	if params["boundary"] == "" {
		return ErrInvalidResourceSpec
	}
	reader := multipart.NewReader(body, params["boundary"])
	count := 0
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF && count > 0 {
			return nil
		}
		if err != nil {
			return ErrInvalidResourceSpec
		}
		if err := validateKTMultipart(part.Header, part, depth+1); err != nil {
			return ErrInvalidResourceSpec
		}
		count++
	}
}
