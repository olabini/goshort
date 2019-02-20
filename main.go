package main

import (
	"bufio"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

var (
	ServerNameConfig      = flag.String("server-name", "http://localhost", "The public name of the URL shortener service, including protocol, and optionally port")
	SecretConfig          = flag.String("secret", "changeme", "The secret that has to be submitted to be able to create a new shortened URL")
	SpaceConfig           = flag.Int("space", 5, "The number of characters for links created, using a-zA-Z0-9. The default allows for roughly 900,000,000 links")
	ListenHostConfig      = flag.String("host", "localhost", "The host to listen for connections")
	ListenPortConfig      = flag.String("port", "9997", "The port to listen for connections")
	FilenameStorageConfig = flag.String("storage-file", ".goshort.urls.config", "The file in where to store all shortened URLs so far. This will only be read at startup, but written every time a new URL is created")
)

// This only supports HEAD and GET requests through shortened URLs
// POST is reserved to create new shortened URLs
// It is not safe to run this without TLS - so it should be in front of a reverse proxy
// The storage format allows for different sizes of the slug. Thus it's possible to change your mind
// The storage separates the slug from the url using a simple space.

var storage map[string]string
var storageReverse map[string]string
var storageMutex sync.RWMutex

func init() {
	storage = make(map[string]string)
	storageReverse = make(map[string]string)
}

func readStorage() {
	f, e := os.Open(*FilenameStorageConfig)
	if e != nil {
		// No file exists, probably
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		pieces := strings.SplitN(scanner.Text(), " ", 2)
		if len(pieces) == 2 {
			storage[pieces[0]] = pieces[1]
			storageReverse[pieces[1]] = pieces[0]
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "reading storage file: %s - %v\n", *FilenameStorageConfig, err)
	}
}

func fileExists(filename string) bool {
	_, err := os.Stat(filename)
	return err == nil
}

func writeStorage() {
	name := *FilenameStorageConfig
	aname, _ := filepath.Abs(name)
	dir := filepath.Dir(aname)
	f, e := ioutil.TempFile(dir, "goshort-storage")
	if e != nil {
		fmt.Fprintf(os.Stderr, "creating temporary storage file: %v\n", e)
		return
	}

	for slug, url := range storage {
		fmt.Fprintf(f, "%s %s\n", slug, strings.Replace(url, "\n", "", -1))
	}

	f.Close()

	if fileExists(name) {
		os.Remove(name)
	}

	os.Rename(f.Name(), name)
}

const allSlugPossibilities = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func oneSlugEntry() rune {
	return rune(allSlugPossibilities[rand.Intn(len(allSlugPossibilities))])
}

func genSlug() string {
	entries := make([]rune, *SpaceConfig)
	for ix := range entries {
		entries[ix] = oneSlugEntry()
	}
	return string(entries)
}

func genUniqueSlug() string {
	ix := 0
	for ix < 100000 {
		s := genSlug()
		if _, ok := storage[s]; !ok {
			return s
		}
		ix += 1
	}
	panic("Tried generating 100,000 slugs, and couldn't find one...")
}

func invalidSlug(slug string) bool {
	for _, char := range slug {
		if !strings.Contains(allSlugPossibilities, string(char)) {
			return true
		}
	}
	return false
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	flag.Parse()
	readStorage()
	fmt.Fprintf(os.Stdout, "GoShort starting... we have %d URLs shortened so far\n", len(storage))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		purl, _ := url.ParseRequestURI(r.RequestURI)
		path := purl.Path

		if r.Method == "POST" && path == "/submit" {
			secret := r.PostFormValue("secret")
			url := r.PostFormValue("url")
			slug := r.PostFormValue("slug")
			if secret == *SecretConfig && url != "" {
				storageMutex.Lock()
				defer storageMutex.Unlock()

				existingSlug, existsReverse := storageReverse[url]
				if existsReverse {
					w.Write([]byte(fmt.Sprintf("%s/%s", *ServerNameConfig, existingSlug)))
				} else {
					_, exists := storage[slug]
					if slug == "" || invalidSlug(slug) || exists {
						slug = genUniqueSlug()
					}
					storage[slug] = url
					writeStorage()
					w.Write([]byte(fmt.Sprintf("%s/%s", *ServerNameConfig, slug)))
					fmt.Fprintf(os.Stdout, " - added new shortening: %s for %s\n", slug, url)
				}
			} else {
				http.Error(w, "Not authorized", http.StatusUnauthorized)
			}
		} else if r.Method == "GET" || r.Method == "HEAD" {
			slug := strings.TrimPrefix(path, "/")
			storageMutex.RLock()
			url, ok := storage[slug]
			storageMutex.RUnlock()
			if ok {
				http.Redirect(w, r, string(url), http.StatusMovedPermanently)
			} else {
				http.NotFound(w, r)
			}
		} else {
			http.NotFound(w, r)
		}
	})

	log.Fatal(http.ListenAndServe(net.JoinHostPort(*ListenHostConfig, *ListenPortConfig), nil))
}
