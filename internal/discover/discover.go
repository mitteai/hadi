// Package discover resolves services to boxes. DNS is the registry: terraform
// publishes <name>.boxes.<zone> (one A record per box) and _hadi.<zone> (TXT,
// the list of hadi-managed services). One resolver call, no API client, no
// token, no cache.
package discover

import (
	"fmt"
	"net"
	"sort"
	"strings"
)

// Boxes returns the box addresses for a service: the config's hosts override
// when present, else the boxes record. --host trumps both (handled by caller).
func Boxes(name, zone string, hosts []string) ([]string, error) {
	if len(hosts) > 0 {
		return hosts, nil
	}
	fqdn := name + ".boxes." + zone
	ips, err := net.LookupHost(fqdn)
	if err != nil {
		return nil, fmt.Errorf("discovery: %s did not resolve (%w). Is the record published from terraform, or should deploy.json set \"hosts\"?", fqdn, err)
	}
	sort.Strings(ips)
	return ips, nil
}

// Services lists hadi-managed service names from the _hadi.<zone> TXT record.
func Services(zone string) ([]string, error) {
	txts, err := net.LookupTXT("_hadi." + zone)
	if err != nil {
		return nil, fmt.Errorf("fleet listing: _hadi.%s did not resolve (%w). Publish it from terraform: one TXT record listing the service names.", zone, err)
	}
	var names []string
	for _, t := range txts {
		for _, n := range strings.Split(t, ",") {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
	}
	sort.Strings(names)
	return names, nil
}
