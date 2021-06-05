package restserver

import (
	"io/ioutil"
	"os"
	"testing"
)

func TestValidate(t *testing.T) {
	user := "restic"
	pwd := "$2y$05$z/OEmNQamd6m6LSegUErh.r/Owk9Xwmc5lxDheIuHY2Z7XiS6FtJm"
	rawPwd := "test"
	wrongPwd := "wrong"

	tmpfile, err := ioutil.TempFile("", "rest-validate-")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tmpfile.Write([]byte(user + ":" + pwd + "\n")); err != nil {
		t.Fatal(err)
	}
	if err = tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	htpass, err := NewHtpasswdFromFile(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 10; i++ {
		isValid := htpass.Validate(user, rawPwd)
		if !isValid {
			t.Fatal("correct password not accepted")
		}

		isValid = htpass.Validate(user, wrongPwd)
		if isValid {
			t.Fatal("wrong password accepted")
		}
	}

	if err = os.Remove(tmpfile.Name()); err != nil {
		t.Fatal(err)
	}
}
