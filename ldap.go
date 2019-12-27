package restserver

/*
MIT License

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
*/

import (
	"crypto/tls"
	"fmt"
	"log"

	"github.com/coocood/freecache"
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/ldap.v2"
)

type LdapAuth struct {
	LdapConfig LdapConf
	cache      *freecache.Cache
	debug      bool
}

func NewLdapAuth(debug bool) *LdapAuth {
	l := &LdapAuth{
		LdapConfig: NewLdapConfig(),
		cache:      freecache.NewCache(256 * 1024 * 1024),
		debug:      debug,
	}
	return l
}

func (a *LdapAuth) hashAndSalt(pwd string) []byte {
	hash, err := bcrypt.GenerateFromPassword([]byte(pwd), bcrypt.MinCost)
	if err != nil {
		log.Println(err)
	}
	if a.debug {
		log.Printf("message=bcrypt encrypted result=%v", string(hash))
	}
	return hash
}

func (a *LdapAuth) ValidateOld(username string, password string) bool {
	return a.validateWithLdap(username, password)
}

func (a *LdapAuth) Validate(username string, password string) bool {
	authenticated, err := a.validateWithCache(username, password)
	if err != nil {
		pwdValid := a.validateWithLdap(username, password)
		if pwdValid {
			a.cacheCredentials(username, password)
			return true
		} else {
			return false
		}
	} else {
		return authenticated
	}
}

func (a *LdapAuth) cacheCredentials(username string, password string) {
	key := []byte(username)
	val := a.hashAndSalt(password)
	expire := 120 // expire in 120 seconds
	a.cache.Set(key, val, expire)
	return
}

func (a *LdapAuth) validateWithCache(username string, password string) (bool, error) {
	pwdHash, err := a.cache.Get([]byte(username))
	if err != nil {
		return false, err
	}
	err = bcrypt.CompareHashAndPassword(pwdHash, []byte(password))
	if err == nil {
		if a.debug {
			log.Printf("message=credentials validated from cache result=success")
		}
		return true, nil
	}
	return false, err
}

func (a *LdapAuth) validateWithLdap(username string, password string) bool {
	ldapFilter := a.userFilter(username)

	bindusername := a.LdapConfig.LdapSearchDn
	bindpassword := a.LdapConfig.LdapSearchPassword

	l, err := ldap.Dial("tcp", fmt.Sprintf("%s:%d", a.LdapConfig.LdapURL, 389))
	if err != nil {
		log.Printf("message=ldap connect failed error=%v", err)
		return false
	}
	defer l.Close()

	// Upgrade to TLS
	err = l.StartTLS(&tls.Config{InsecureSkipVerify: true})
	if err != nil {
		log.Printf("message=tls upgrade failed error=%v", err)
		return false
	}

	// First bind with a read only user
	err = l.Bind(bindusername, bindpassword)
	if err != nil {
		log.Printf("message=bind with SearchDn failed user=%s password=%s error=%v", bindusername, bindpassword, err)
		return false
	}

	// Search for the given username
	searchRequest := ldap.NewSearchRequest(
		a.LdapConfig.LdapBaseDn,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		0,     //Unlimited results
		0,     //Search Timeout
		false, //Types only
		ldapFilter,
		[]string{"dn"},
		nil,
	)

	sr, err := l.Search(searchRequest)
	if err != nil {
		log.Printf("message=search failed error=%v", err)
		return false
	}

	if len(sr.Entries) != 1 {
		log.Printf("message=search has none or more than one result error=%v", err)
		return false
	}

	userdn := sr.Entries[0].DN

	// Bind as the user to verify their password
	err = l.Bind(userdn, password)
	if err != nil {
		log.Printf("message=bind failed error=%v", err)
		return false
	} else {
		if a.debug {
			log.Printf("message=bind succeeded")
		}
		return true
	}

	return false

}

func (a *LdapAuth) userFilter(username string) string {
	filterWith := ldap.EscapeFilter(username)
	ldapFilter := a.LdapConfig.LdapFilter

	if ldapFilter == "" {
		ldapFilter = "(" + a.LdapConfig.LdapUID + "=" + filterWith + ")"
	} else {
		ldapFilter = "(&" + a.LdapConfig.LdapFilter + "(" + a.LdapConfig.LdapUID + "=" + filterWith + "))"
	}

	if a.debug {
		log.Printf("message=apply filter ldapFilter=%v", ldapFilter)
	}
	return ldapFilter
}
