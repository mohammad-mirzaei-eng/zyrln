package route

import (
	"bufio"
	"bytes"
	"embed"
	"fmt"
	"io"
	"strings"
)

// Bundled domain list (refresh by replacing domesticdata/domains.txt before release).
//
//go:embed domesticdata/domains.txt
var domesticData embed.FS

func loadBundledDomesticRules() error {
	domainsBytes, err := domesticData.ReadFile("domesticdata/domains.txt")
	if err != nil {
		return fmt.Errorf("bundled domains: %w", err)
	}
	if err := parseDomainsText(bytes.NewReader(domainsBytes)); err != nil {
		return fmt.Errorf("bundled domains parse: %w", err)
	}
	return nil
}

func parseDomainsText(r io.Reader) error {
	m := &domesticMatcher{}
	sc := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m.addRoot(line)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	domesticRules.Store(m)
	return nil
}
