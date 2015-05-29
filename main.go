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
	"strings"
	"time"
)

const (
	UPLOADED_FILE_HASH_SIZE = 40
	REPO_ID_SIZE            = 36
	PATH_DOESNT_EXIST_MSG   = "Path does not exist"
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

type FileSpec struct {
	Id    string        `json:"id"`
	MTime time.Duration `json:"mtime"`
	Type  string        `json:"type"`
	Name  string        `json:"name"`
	Size  int64         `json:"size"`
}

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

func DoSeafileRequest(method, path string) ([]byte, error) {
	method_url := seafile_url + path

	client := &http.Client{}

	req, err := http.NewRequest(method, method_url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Authorization", "Token "+token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil && err != io.EOF {
		return nil, err
	}

	return data, nil
}

func DoSeafileRequestJSON(method, path string, returnJSON interface{}) error {
	data, err := DoSeafileRequest(method, path)

	if err != nil {
		return err
	}

	return json.Unmarshal(data, &returnJSON)
}

// curl -H 'Authorization: Token 24fd3c026886e3121b2ca630805ed425c272cb96' https://cloud.seafile.com/api2/auth/ping/
// "pong"
func PingAuth() error {
	var jsonData string
	err := DoSeafileRequestJSON("GET", "/api2/auth/ping/", &jsonData)

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

	err := DoSeafileRequestJSON("GET", "/api2/default-repo/", &dat)

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

// Download File
// curl  -v  -H 'Authorization: Token f2210dacd9c6ccb8133606d94ff8e61d99b477fd' -H 'Accept: application/json; charset=utf-8; indent=4' https://cloud.seafile.com/api2/repos/dae8cecc-2359-4d33-aa42-01b7846c4b32/file/?p=/foo.c
// "https://cloud.seafile.com:8082/files/adee6094/foo.c"
func GetDownloadFileLink(path string) (string, error) {
	params := url.Values{"p": {path}}
	var result interface{}

	api_path := "/api2/repos/" + default_repo + "/file/?" + params.Encode()
	err := DoSeafileRequestJSON("GET", api_path, &result)
	if err != nil {
		return "", err
	}

	switch result.(type) {
	case string:
		return result.(string), nil
	case map[string]interface{}:
		hash := (result).(map[string]interface{})
		error_msg := hash["error_msg"]
		switch error_msg.(type) {
		case string:
			return "", errors.New((error_msg).(string))
		default:
			return "", errors.New(fmt.Sprintf("Unknown response: %v", result))
		}
	default:
		return "", errors.New(fmt.Sprintf("Unknown response: %v", result))
	}
}

// curl -H "Authorization: Token f2210dacd9c6ccb8133606d94ff8e61d9b477fd" -H 'Accept: application/json; indent=4' https://cloud.seafile.com/api2/repos/99b758e6-91ab-4265-b705-925367374cf0/dir/?p=/foo
// If oid is the latest oid of the directory, returns "uptodate" , else returns
// [
// {
//     "id": "0000000000000000000000000000000000000000",
//     "type": "file",
//     "name": "test1.c",
//     "size": 0
// },
// {
//     "id": "e4fe14c8cda2206bb9606907cf4fca6b30221cf9",
//     "type": "dir",
//     "name": "test_dir"
// }
// ]
func ListDirectory(directory string) (err error, files []string) {
	params := url.Values{"p": {directory}}
	path := "/api2/repos/" + default_repo + "/dir/?" + params.Encode()

	data, err := DoSeafileRequest("GET", path)
	if err != nil {
		return err, nil
	}

	var filespecs []FileSpec
	if err := json.Unmarshal(data, &filespecs); err == nil {
		for _, entry := range filespecs {
			if entry.Type == "file" {
				files = append(files, entry.Name)
			}
		}

		return nil, files
	}

	msg := fmt.Sprintf("Unknown server response: %v", string(data))

	var hash map[string]string
	if err := json.Unmarshal(data, &hash); err == nil {
		if hash["error_msg"] != "" {
			msg = hash["error_msg"]
		}
	}

	return errors.New(msg), nil
}

func IsDirectoryExist(directory string) (error, []string, bool) {
	err, files := ListDirectory(directory)

	if err == nil {
		return nil, files, true
	}

	if err.Error() == PATH_DOESNT_EXIST_MSG {
		return nil, nil, false
	} else {
		return err, nil, false
	}
}

// curl -d  "operation=mkdir" -v  -H 'Authorization: Tokacd9c6ccb8133606d94ff8e61d99b477fd' -H 'Accept: application/json; charset=utf-8; indent=4' https://cloud.seafile.com/api2/repos/dae8cecc-2359-4d33-aa42-01b7846c4b32/dir/?p=/foo
// ...
// < HTTP/1.0 201 CREATED
// < Location: https://cloud.seafile.com/api2/repos/dae8cecc-2359-4d33-aa42-01b7846c4b32/dir/?p=/foo
// ...
// "success"
func CreateDirectory(directory string) error {
	params := url.Values{"p": {directory}}
	url_with_params := seafile_url + "/api2/repos/" + default_repo + "/dir/?" + params.Encode()

	log.Println("POST", url_with_params)

	request_body := "operation=mkdir"
	req, err := http.NewRequest("POST", url_with_params, strings.NewReader(request_body))

	if err != nil {
		return err
	}
	req.Header.Add("Authorization", "Token "+token)
	req.Header.Add("Accept", "application/json; charset=utf-8")
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Add("Content-Length", fmt.Sprintf("%d", len(request_body)))

	client := &http.Client{}

	resp, err := client.Do(req)

	if err != nil {
		return err
	}

	response_body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	resp.Body.Close()
	response := string(response_body)
	log.Println(response)

	if response != "\"success\"" {
		var returnData map[string]string
		err = json.Unmarshal(response_body, &returnData)
		if err != nil {
			return err
		}

		if returnData["error_msg"] != "" {
			return errors.New("Cannot create directory " + directory + " > " + returnData["error_msg"])
		}
	}

	return nil
}

// Gets link where to upload file.
// GET https://cloud.seafile.com/api2/repos/{repo-id}/upload-link/
// curl -H "Authorization: Token f2210dacd9c6ccb8133606d94ff8e61d99b477fd" https://cloud.seafile.com/api2/repos/99b758e6-91ab-4265-b705-925367374cf0/upload-link/
// "http://cloud.seafile.com:8082/upload-api/ef881b22"
func GetUploadLink() error {
	return DoSeafileRequestJSON("GET", "/api2/repos/"+default_repo+"/upload-link/", &upload_link)
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
func UploadFile(src io.Reader, folder, filename, callback_url string) error {
	log.Println("Uploading", folder+filename)

	request_body := &bytes.Buffer{}
	multipart_writer := multipart.NewWriter(request_body)
	part, err := multipart_writer.CreateFormFile("file", filename)
	if err != nil {
		return err
	}
	_, err = io.Copy(part, src)

	multipart_writer.WriteField("filename", filename)
	multipart_writer.WriteField("parent_dir", folder)

	err = multipart_writer.Close()
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", upload_link, request_body)
	if err != nil {
		return err
	}
	req.Header.Add("Authorization", "Token "+token)
	req.Header.Set("Content-Type", multipart_writer.FormDataContentType())

	client := &http.Client{}

	resp, err := client.Do(req)

	if err != nil {
		return err
	}

	response_body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	resp.Body.Close()
	response := string(response_body)

	if len(response) != UPLOADED_FILE_HASH_SIZE {
		err_msg := fmt.Sprintf("Cannot upload %s", folder+filename)
		log.Println(err_msg)
		return errors.New(err_msg)
	}

	log.Println("Saved", response, folder+filename)

	if callback_url != "" {
		go func() {
			params := url.Values{"folder": {folder}, "file": {filename}, "hash": {response}}
			url_with_params := callback_url + "?" + params.Encode()
			_, err := http.Get(url_with_params)
			if err != nil {
				log.Println(err.Error())
				return
			}
			log.Println("Called back to", callback_url)
		}()
	}

	return nil
}

// Web-server part.

//Display the named template
func display(w http.ResponseWriter, tmpl string, data interface{}) {
	templates.ExecuteTemplate(w, tmpl+".html", data)
}

var MAX_FORM_SIZE int64 = 1024 * 1024 * 1024 // 1GB

func fetchValue(values []string, defaultValue string) (value string) {
	value = defaultValue

	if len(values) > 0 && values[0] != "" {
		value = values[0]
	}

	return
}

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

		dir := fetchValue(form.Value["folder"], "/test/")
		callback_url := fetchValue(form.Value["callback"], "http://localhost:3000/seafile_uploads")

		err, files_exist, dir_exist := IsDirectoryExist(dir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if !dir_exist {
			if err := CreateDirectory(dir); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		files := form.File["file"]
		uploaded := 0
		for i, f := range files {
			found := false
			for _, fe := range files_exist {
				if f.Filename == fe {
					log.Println("Skipping", dir+fe)
					found = true
					break
				}
			}
			if found {
				continue
			}

			//for each fileheader, get a handle to the actual file
			file, err := files[i].Open()
			defer file.Close()

			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			err = UploadFile(file, dir, f.Filename, callback_url)

			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			uploaded++
		}

		time_taken := time.Since(start)

		//display success message.
		msg := fmt.Sprintf("Upload successful. Time taken: %v. Uploaded %v files", time_taken, uploaded)
		display(w, "upload", msg)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func downloadHandler(w http.ResponseWriter, r *http.Request) {
	log.Println(r.Method, r.RequestURI)
	switch r.Method {
	case "GET":
		request_uri, err := url.ParseRequestURI(r.RequestURI)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		path := strings.Replace(request_uri.Path, "/get/", "/", 1)

		link, err := GetDownloadFileLink(path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		sfr, err := http.NewRequest("GET", link, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		headers_to_forward := []string{"If-Modified-Since", "Accept", "Accept-Encoding", "Accept-Language", "Cache-Control", "Pragma"}
		for _, header := range headers_to_forward {
			header_value_from_request := r.Header.Get(header)
			if header_value_from_request != "" {
				sfr.Header.Add(header, header_value_from_request)
			}
		}

		client := &http.Client{}
		resp, err := client.Do(sfr)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		if r.Header.Get("Connection") == "keep-alive" {
			w.Header().Add("Connection", "keep-alive")
		}

		switch resp.StatusCode {
		case 200:
			headers_to_return := []string{"Cache-Control", "Last-Modified"}
			w.Header().Add("Access-Control-Allow-Origin", "*")

			for _, header := range headers_to_return {
				header_value_from_response := resp.Header.Get(header)
				if header_value_from_response != "" {
					w.Header().Add(header, header_value_from_response)
				}
			}

			// Cache-Control:max-age=3600
			var buf_size int64 = 1024 * 1024 // 1MB

			for {
				_, err := io.CopyN(w, resp.Body, buf_size)

				if err != nil {
					if err == io.EOF {
						break
					} else {
						// Connection was interrupted.
						return
					}
				}

				if f, ok := (w).(http.Flusher); ok {
					f.Flush()
				}
			}

		// Status "Not modified" is here too.
		default:
			http.Error(w, resp.Status, resp.StatusCode)
			return
		}

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// Start web server after configuration.
func StartWebServer() {
	http.HandleFunc("/upload", uploadHandler)
	http.HandleFunc("/get/", downloadHandler)

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
