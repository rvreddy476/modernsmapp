package invoice

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// PDFRenderer renders invoices as PDF by piping HTML through wkhtmltopdf.
// Binary is resolved via the INVOICE_WKHTMLTOPDF_BIN env (defaults to "wkhtmltopdf").
// If the binary is missing at render time, Render returns an error and callers
// should fall back to HTMLRenderer.
type PDFRenderer struct {
	// BinPath overrides the executable path. Empty = env/default lookup.
	BinPath string
	// ExtraArgs are appended to the wkhtmltopdf command line.
	ExtraArgs []string
}

func (r PDFRenderer) Render(inv Invoice) ([]byte, string, error) {
	htmlBody, _, err := HTMLRenderer{}.Render(inv)
	if err != nil {
		return nil, "", err
	}
	bin := r.BinPath
	if bin == "" {
		bin = os.Getenv("INVOICE_WKHTMLTOPDF_BIN")
	}
	if bin == "" {
		bin = "wkhtmltopdf"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return nil, "", fmt.Errorf("%s not found: %w", bin, err)
	}

	// wkhtmltopdf reads HTML from stdin ("-") and writes PDF to stdout ("-").
	args := append([]string{"--quiet", "--enable-local-file-access"}, r.ExtraArgs...)
	args = append(args, "-", "-")
	cmd := exec.Command(bin, args...)
	cmd.Stdin = bytes.NewReader(htmlBody)

	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, "", fmt.Errorf("wkhtmltopdf: %w: %s", err, stderr.String())
	}
	if out.Len() == 0 {
		return nil, "", errors.New("wkhtmltopdf produced empty output")
	}
	return out.Bytes(), "application/pdf", nil
}
