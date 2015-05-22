package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lazureykis/dotenv"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
)

// Application configuration

var (
	// Seafile API endpoint. For example: "https://my-seafile-host.com"
	seafile_url string

	// User authorization token
	token string

	// TCP address to listen. For example: :8080
	listen string

	// All stored files remains in this library.
	default_repo string
)

func ConfigureApp() {
	dotenv.Go()

	token = os.Getenv("SEAFILE_TOKEN")
	seafile_url = os.Getenv("SEAFILE_URL")
	listen = os.Getenv("SEAFILE_PROXY_LISTEN")

	if seafile_url == "" {
		log.Fatalln("SEAFILE_URL is blank.\nYou should pass url to your seafile host in SEAFILE_URL variable.\n For example: SEAFILE=https://yourhost.com")
	}

	if listen == "" {
		listen = ":8881"
	}

	if len(os.Args) < 2 || os.Args[1] != "login" {
		if token == "" {
			log.Fatalln("SEAFILE_TOKEN is blank.\nYou should pass SEAFILE_TOKEN environment variable.\nRun 'seafile login your_username your_password' to get authentication token.")
		} else {
			if err := PingAuth(); err != nil {
				log.Fatalln(err)
			}
		}
	}

	if err := GetDefaultRepo(); err != nil {
		log.Fatalln(err)
	}
}

func DoSeafileRequest(method, path string, v interface{}) error {
	method_url := seafile_url + path

	client := &http.Client{}

	req, err := http.NewRequest(method, method_url, nil)
	if err != nil {
		return err
	}

	req.Header.Add("Authorization", "Token "+token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	reader := bufio.NewReader(resp.Body)
	data, err := reader.ReadBytes('\n')
	if err != nil && err != io.EOF {
		return err
	}

	err = json.Unmarshal(data, &v)

	if err != nil {
		return err
	}

	return nil
}

// curl -H 'Authorization: Token 24fd3c026886e3121b2ca630805ed425c272cb96' https://cloud.seafile.com/api2/auth/ping/
// "pong"
func PingAuth() error {
	var jsonData string
	err := DoSeafileRequest("GET", "/api2/auth/ping/", &jsonData)

	if err != nil {
		return err
	}

	if jsonData != "pong" {
		return errors.New("Ping was replied with: " + jsonData)
	}

	return nil
}

//
// Login command used to get authorization token
//
// curl -d "username=username@example.com&password=123456" https://cloud.seafile.com/api2/auth-token/
// {"token": "24fd3c026886e3121b2ca630805ed425c272cb96"}
func Login(username, password string) (err error) {
	path := seafile_url + "/api2/auth-token/"
	resp, err := http.PostForm(path, url.Values{"username": {username}, "password": {password}})

	if err != nil {
		return err
	}

	bodyReader := bufio.NewReader(resp.Body)
	var bodyData []byte
	bodyData, err = bodyReader.ReadBytes('\n')

	if err != nil && err != io.EOF {
		return err
	}

	var dat map[string]interface{}
	err = json.Unmarshal(bodyData, &dat)

	if err != nil {
		return err
	}

	if dat["non_field_errors"] != nil && len(dat["non_field_errors"].([]interface{})) > 0 {
		return errors.New(dat["non_field_errors"].([]interface{})[0].(string))
	}

	if len(dat["token"].(string)) == 0 {
		return errors.New("No token returned.")
	}

	token = dat["token"].(string)
	return nil
}

// Helper method to get token by username and password.
func MaybeLoginRequest() {
	if len(os.Args) > 1 && os.Args[1] == "login" {
		if len(os.Args) < 4 {
			log.Fatalln("USAGE: seafile-uploader login username password")
		}

		err := Login(os.Args[2], os.Args[3])

		if err != nil {
			log.Fatalln(token, err)
		}

		fmt.Println("Your token:", token)

		os.Exit(0)
	}
}

//
// Get default library identifier
//
// curl -H 'Authorization: Token f2210dacd9c6ccb8133606d94ff8e61d99b477fd' "https://cloud.seafile.com/api2/default-repo/"
// {
//     "repo_id": "691b3e24-d05e-43cd-a9f2-6f32bd6b800e",
//     "exists": true
// }
func GetDefaultRepo() error {
	var dat map[string]interface{}

	err := DoSeafileRequest("GET", "/api2/default-repo/", &dat)

	if err != nil {
		return err
	}

	if !(dat["exists"].(bool)) {
		return errors.New("Repo doesn't exists")
	}

	default_repo = dat["repo_id"].(string)

	if len(default_repo) != 36 {
		return errors.New("Invalid default_repo: " + default_repo)
	}

	return nil
}

// Web-server part.

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, "")
}

// Start web server after configuration.
func StartWebServer() {
	http.HandleFunc("/", uploadHandler)

	log.Printf("Started on %s.\n", listen)
	log.Fatal(http.ListenAndServe(listen, nil))
}

func main() {
	ConfigureApp()
	MaybeLoginRequest()
	StartWebServer()
}
