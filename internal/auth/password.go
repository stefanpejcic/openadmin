// Password hashing compatible with Werkzeug's hash format, so existing hashes in users.db keep working without a forced reset.
//
// Format is "<method>$<salt>$<hash_hex>", with two methods in the wild:
//   - "scrypt:n:r:p" (bare "scrypt" defaults n=32768, r=8, p=1), 64-byte key
//   - "pbkdf2:hash_name:iterations" (bare "pbkdf2" defaults sha256/600000), key length is the hash's own size
//
// a "$6$" hash is glibc's SHA-512-crypt instead (what `opencli admin new`/`password` produce) and is verified separately since its salt is "$"-delimited from an optional rounds= prefix, not the shape above
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"math/big"
	"strconv"
	"strings"

	"github.com/GehirnInc/crypt"
	_ "github.com/GehirnInc/crypt/sha512_crypt"
	"golang.org/x/crypto/pbkdf2"
	"golang.org/x/crypto/scrypt"
)

const saltAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// CheckPasswordHash reports whether password matches the given salted hash.
func CheckPasswordHash(pwhash, password string) bool {
	if strings.HasPrefix(pwhash, "$6$") {
		return crypt.SHA512.New().Verify(pwhash, []byte(password)) == nil
	}

	parts := strings.SplitN(pwhash, "$", 3)
	if len(parts) != 3 {
		return false
	}
	method, salt, wantHash := parts[0], parts[1], parts[2]

	got, err := hashInternal(method, salt, password)
	if err != nil {
		return false
	}
	return hmac.Equal([]byte(got), []byte(wantHash))
}

// GeneratePasswordHash hashes password using scrypt:32768:8:1 with a 16-character salt
func GeneratePasswordHash(password string) (string, error) {
	salt, err := randomSalt(16)
	if err != nil {
		return "", err
	}
	h, err := hashInternal("scrypt:32768:8:1", salt, password)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("scrypt:32768:8:1$%s$%s", salt, h), nil
}

func hashInternal(method, salt, password string) (string, error) {
	fields := strings.Split(method, ":")
	name, args := fields[0], fields[1:]

	switch name {
	case "scrypt":
		n, r, p := 32768, 8, 1
		if len(args) == 3 {
			var err error
			if n, err = strconv.Atoi(args[0]); err != nil {
				return "", err
			}
			if r, err = strconv.Atoi(args[1]); err != nil {
				return "", err
			}
			if p, err = strconv.Atoi(args[2]); err != nil {
				return "", err
			}
		} else if len(args) != 0 {
			return "", fmt.Errorf("'scrypt' takes 3 arguments")
		}
		key, err := scrypt.Key([]byte(password), []byte(salt), n, r, p, 64)
		if err != nil {
			return "", err
		}
		return hex.EncodeToString(key), nil

	case "pbkdf2":
		hashName, iterations := "sha256", 600000
		switch len(args) {
		case 0:
		case 1:
			hashName = args[0]
		case 2:
			hashName = args[0]
			var err error
			if iterations, err = strconv.Atoi(args[1]); err != nil {
				return "", err
			}
		default:
			return "", fmt.Errorf("'pbkdf2' takes 2 arguments")
		}
		newHash, err := pbkdf2HashFunc(hashName)
		if err != nil {
			return "", err
		}
		key := pbkdf2.Key([]byte(password), []byte(salt), iterations, newHash().Size(), newHash)
		return hex.EncodeToString(key), nil

	default:
		return "", fmt.Errorf("invalid hash method %q", name)
	}
}

func pbkdf2HashFunc(name string) (func() hash.Hash, error) {
	switch name {
	case "sha1":
		return sha1.New, nil
	case "sha256":
		return sha256.New, nil
	case "sha512":
		return sha512.New, nil
	default:
		return nil, fmt.Errorf("unsupported pbkdf2 hash %q", name)
	}
}

func randomSalt(n int) (string, error) {
	b := make([]byte, n)
	for i := range b {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(saltAlphabet))))
		if err != nil {
			return "", err
		}
		b[i] = saltAlphabet[idx.Int64()]
	}
	return string(b), nil
}
