package restserver

import (
	"os"
)

type LdapConf struct {
	LdapURL               string
	LdapSearchDn          string
	LdapSearchPassword    string
	LdapBaseDn            string
	LdapFilter            string
	LdapUID               string
	LdapScope             int
	LdapConnectionTimeout int
	LdapVerifyCert        bool
}

func NewLdapConfig() LdapConf {
	newLdapConfig := LdapConf{
		LdapURL:            os.Getenv("LDAP_URL"),
		LdapSearchDn:       os.Getenv("LDAP_SEARCHDN"),
		LdapSearchPassword: os.Getenv("LDAP_SEARCHPWD"),
		LdapBaseDn:         os.Getenv("LDAP_BASEDN"),
		LdapFilter:         os.Getenv("LDAP_FILTER"),
		LdapUID:            os.Getenv("LDAP_UID"),
	}
	return newLdapConfig
}
