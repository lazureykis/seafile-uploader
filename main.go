package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lazureykis/dotenv"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"time"
)

const (
	UPLOADED_FILE_HASH_SIZE = 40
	REPO_ID_SIZE            = 36
)

// Application configuration
var (
	//Compile templates on start
	templates = template.Must(template.ParseFiles("tmpl/upload.html"))

	// Seafile API endpoint. For example: "https://my-seafile-host.com"
	seafile_url string

	// User authorization token
	token string

	// TCP address to listen. For example: :8080
	listen string

	// All stored files remains in this library.
	default_repo string

	// Seafile Upload API HTTP address
	upload_link string
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

	if err := GetUploadLink(); err != nil {
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
	defer resp.Body.Close()

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

	defer resp.Body.Close()

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

	if len(default_repo) != REPO_ID_SIZE {
		return errors.New("Invalid default_repo: " + default_repo)
	}

	return nil
}

// Gets link where to upload file.
// GET https://cloud.seafile.com/api2/repos/{repo-id}/upload-link/
// curl -H "Authorization: Token f2210dacd9c6ccb8133606d94ff8e61d99b477fd" https://cloud.seafile.com/api2/repos/99b758e6-91ab-4265-b705-925367374cf0/upload-link/
// "http://cloud.seafile.com:8082/upload-api/ef881b22"
func GetUploadLink() error {
	return DoSeafileRequest("GET", "/api2/repos/"+default_repo+"/upload-link/", &upload_link)
}

// UploadFile API request.
// Errors:
// 400 Bad request
// 440 Invalid filename
// 441 File already exists
// 500 Internal server error
//
// Sample:
// curl -H "Authorization: Token f2210dacd9c6ccb8133606d94ff8e61d99b477fd" -F file=@test.txt -F filename=test.txt -F parent_dir=/ http://cloud.seafile.com:8082/upload-api/ef881b22
// "adc83b19e793491b1c6ea0fd8b46cd9f32e592fc"
func UploadFile(src io.Reader, folder, filename string) error {
	log.Println("Uploading", folder+filename)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return err
	}
	_, err = io.Copy(part, src)

	writer.WriteField("filename", filename)
	writer.WriteField("parent_dir", folder)

	err = writer.Close()
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", upload_link, body)
	if err != nil {
		return err
	}
	req.Header.Add("Authorization", "Token "+token)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{}

	resp, err := client.Do(req)

	if err != nil {
		return err
	}

	// TODO: parse response.
	rbody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	resp.Body.Close()
	file_hash := string(rbody)

	if len(file_hash) != UPLOADED_FILE_HASH_SIZE {
		err_msg := fmt.Sprintf("Cannot upload %s", folder+filename)
		log.Println(err_msg)
		return errors.New(err_msg)
	}

	log.Println("Saved", file_hash, folder+filename)

	return nil
}

// Web-server part.

//Display the named template
func display(w http.ResponseWriter, tmpl string, data interface{}) {
	templates.ExecuteTemplate(w, tmpl+".html", data)
}

var MAX_FORM_SIZE int64 = 1024 * 1024 * 1024 // 1GB

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	log.Println(r.Method, r.RequestURI)
	switch r.Method {
	//GET displays the upload form.
	case "GET":
		display(w, "upload", nil)

	//POST takes the uploaded file(s) and saves it to disk.
	case "POST":
		start := time.Now()
		content_length := r.Header.Get("Content-Length")
		log.Println("Received", content_length, "bytes")

		err := r.ParseMultipartForm(MAX_FORM_SIZE)

		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		form := r.MultipartForm
		defer form.RemoveAll()

		var dir string
		if len(form.Value["folder"]) > 0 {
			dir = form.Value["folder"][0]
		}

		if dir == "" {
			dir = "/test/"
		}

		files := form.File["file"]
		for i, f := range files {
			//for each fileheader, get a handle to the actual file
			file, err := files[i].Open()
			defer file.Close()

			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			err = UploadFile(file, dir, f.Filename)

			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		time_taken := time.Since(start)

		//display success message.
		msg := fmt.Sprintf("Upload successful. Time taken: %v", time_taken)
		display(w, "upload", msg)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// Start web server after configuration.
func StartWebServer() {
	http.HandleFunc("/upload", uploadHandler)

	//static file handler.
	http.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("assets"))))

	log.Printf("Started on %s.\n", listen)
	log.Fatal(http.ListenAndServe(listen, nil))
}

func main() {
	ConfigureApp()
	MaybeLoginRequest()
	StartWebServer()
}
