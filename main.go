package main

import (
	"cloud.google.com/go/storage"
	"context"
	"encoding/json"
	"fmt"
	jwtmiddleware "github.com/auth0/go-jwt-middleware"
	jwt "github.com/dgrijalva/jwt-go"
	"github.com/gorilla/mux"
	"github.com/olivere/elastic"
	"github.com/pborman/uuid"

	"io"
	"log"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
)

const (
	POST_INDEX = "post"
	DISTANCE   = "200km"

	// GCE internal IP, because it will never change
	ES_URL      = "http://10.128.0.2:9200"
	BUCKET_NAME = "ya-around-bucket"
)

// hashMap to store mapping of media file extensions to types
var (
	mediaTypes = map[string]string{
		".jpeg": "image",
		".jpg":  "image",
		".gif":  "image",
		".png":  "image",
		".mov":  "video",
		".mp4":  "video",
		".avi":  "video",
		".flv":  "video",
		".wmv":  "video",
	}
)

type Location struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type Post struct {
	// `json:"user"` is for the json parsing of this User field. Otherwise, by default it's 'User'.
	User     string   `json:"user"`
	Message  string   `json:"message"`
	Location Location `json:"location"`
	Url      string   `json:"url"`  // url of dedia file in GCS
	Type     string   `json:"type"` // type of media file
	Face     float32  `json:"face"` // probability of face using Google Vision API
}

func main() {
	fmt.Println("started-service")

	// create jwt middleware, specify security and encrytion method here
	jwtMiddleware := jwtmiddleware.New(jwtmiddleware.Options{
		ValidationKeyGetter: func(token *jwt.Token) (interface{}, error) {
			return []byte(mySigningKey), nil
		},
		SigningMethod: jwt.SigningMethodHS256,
	})

	// go httpHandler cannot distinguish get/post method
	// I'll use a third-party library mux to make it restful
	r := mux.NewRouter()
	/*
		http.HandleFunc("/post", handlerPost)
		http.HandleFunc("/search", handlerSearch)
		http.HandleFunc("/cluster", handlerCluster)

		log.Fatal(http.ListenAndServe(":8080", nil))
	*/
	r.Handle("/post", jwtMiddleware.Handler(http.HandlerFunc(handlerPost))).Methods("POST", "OPTIONS")
	r.Handle("/search", jwtMiddleware.Handler(http.HandlerFunc(handlerSearch))).Methods("GET", "OPTIONS")
	r.Handle("/cluster", jwtMiddleware.Handler(http.HandlerFunc(handlerCluster))).Methods("GET", "OPTIONS")
	r.Handle("/signup", http.HandlerFunc(handlerSignup)).Methods("POST", "OPTIONS")
	r.Handle("/login", http.HandlerFunc(handlerLogin)).Methods("POST", "OPTIONS")

	log.Fatal(http.ListenAndServe(":8080", r))
}

/*
 * http handlers
 */
func handlerPost(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Received one post request")

	w.Header().Set("Content-Type", "application/json")
	// allow cross-origin access
	// server returns this header and with allowed domins
	// browser will block this cross-origin access if frontend's domin is not in allowed domins
	// By default it is a blacklist (blocks everyone). * means allow everyone to access the server
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// login
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization")

	// when browser notices it is a cross-origin access, the browser will automatically
	//    send an OPTIONS request to server to check if the server would response with the header
	//    "Acees-Control-Allow-Origin" before actually sending post request to server
	// In this program, since we allowe everyone to access our server, when we receive an OPTIONS request, just return
	if r.Method == "OPTIONS" {
		return
	}

	// extract username from token
	token := r.Context().Value("user")
	claims := token.(*jwt.Token).Claims
	username := claims.(jwt.MapClaims)["username"] // username type is byte[] here

	// read parameter from req.body because it is a post request
	lat, _ := strconv.ParseFloat(r.FormValue("lat"), 64)
	lon, _ := strconv.ParseFloat(r.FormValue("lon"), 64)

	// construct Post
	p := &Post{
		User:    username.(string),
		Message: r.FormValue("message"),
		Location: Location{
			Lat: lat,
			Lon: lon,
		},
	}

	// save media file to GCS
	file, header, err := r.FormFile("image") // header is meta of the file
	if err != nil {
		http.Error(w, "Image is not available", http.StatusBadRequest)
		fmt.Printf("Image is not available %v\n", err)
		return
	}

	suffix := filepath.Ext(header.Filename) // get extension of the file --> type
	if t, ok := mediaTypes[suffix]; ok {
		p.Type = t
	} else {
		p.Type = "unknown"
	}

	id := uuid.New() // generate an unique id for this post, an alternative way is "user_id + upload_time + filename"
	mediaLink, err := saveToGCS(file, id)
	if err != nil {
		http.Error(w, "Failed to save image to GCS", http.StatusInternalServerError)
		fmt.Printf("Failed to save image to GCS %v\n", err)
		return
	}
	p.Url = mediaLink

	// annotate image with vision api
	if p.Type == "image" {
		uri := fmt.Sprintf("gs://%s/%s", BUCKET_NAME, id)
		if score, err := annotate(uri); err != nil {
			http.Error(w, "Failed to annotate image", http.StatusInternalServerError)
			fmt.Printf("Failed to annotate the image %v\n", err)
			return
		} else {
			p.Face = score
		}
	}

	// save to ElasticSearch
	err = saveToES(p, POST_INDEX, id)
	if err != nil {
		http.Error(w, "Failed to save post to Elasticsearch", http.StatusInternalServerError)
		fmt.Printf("Failed to save post to Elasticsearch %v\n", err)
		return
	}
}

func handlerSearch(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Received one search request")

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization")

	if r.Method == "OPTIONS" {
		return
	}

	// frontend send a get request to search, we need to extract query in the URL
	lat, _ := strconv.ParseFloat(r.URL.Query().Get("lat"), 64)
	lon, _ := strconv.ParseFloat(r.URL.Query().Get("lon"), 64)
	ran := DISTANCE
	if val := r.URL.Query().Get("range"); val != "" { // if frontend specified a distance , use that
		ran = val + "km" // safer to specify unit, otherwise ES will use its default unit
	}
	fmt.Println("range is ", ran)

	// create query
	query := elastic.NewGeoDistanceQuery("location")
	query = query.Distance(ran).Lat(lat).Lon(lon)
	searchResult, err := readFromES(query, POST_INDEX)
	if err != nil {
		http.Error(w, "Failed to read post from Elasticsearch", http.StatusInternalServerError)
		fmt.Printf("Failed to read post from Elasticsearch %v.\n", err)
		return
	}

	posts := getPostFromSearchResult(searchResult)
	js, err := json.Marshal(posts)
	if err != nil {
		http.Error(w, "Failed to parse posts into JSON format", http.StatusInternalServerError)
		fmt.Printf("Failed to parse posts into JSON format %v.\n", err)
		return
	}
	w.Write(js)
}

func handlerCluster(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Received one cluster request")

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization")

	if r.Method == "OPTIONS" {
		return
	}

	// extract query called term from URL
	term := r.URL.Query().Get("term")

	// create query
	query := elastic.NewRangeQuery(term).Gte(0.9)

	searchResult, err := readFromES(query, POST_INDEX)
	if err != nil {
		http.Error(w, "Failed to read from Elasticsearch", http.StatusInternalServerError)
		return
	}

	posts := getPostFromSearchResult(searchResult)
	js, err := json.Marshal(posts)
	if err != nil {
		http.Error(w, "Failed to parse post object", http.StatusInternalServerError)
		fmt.Printf("Failed to parse post object %v\n", err)
		return
	}
	w.Write(js)
}

/*
 * connect go server with ElasticSearch
 */

// saveToES: save post/user to ElasticSearch
// parmas: i -> post *Post, or user *User , index -> index name in ElasticSearch, id -> unique id
// returns error if any exists
func saveToES(i interface{}, index string, id string) error {
	client, err := elastic.NewClient(elastic.SetURL(ES_URL))
	if err != nil {
		return err
	}

	// client.Index() -> insert row in ES
	_, err = client.Index().
		Index(index).
		Id(id).
		BodyJson(i).
		Do(context.Background())

	if err != nil {
		return err
	}

	return nil
}

// readFromES: do query and get result from ElasticSearch
// params: query -> ES Query, index -> which table to search in
// returns searchResult in ES
func readFromES(query elastic.Query, index string) (*elastic.SearchResult, error) {
	// create client to ES
	client, err := elastic.NewClient(elastic.SetURL(ES_URL))
	if err != nil {
		return nil, err
	}

	searchResult, err := client.Search().
		Index(index).
		Query(query).
		Pretty(true).
		Do(context.Background()) // Do() return a pointer, searchResult is actually a pointer
	if err != nil {
		return nil, err
	}
	return searchResult, nil
}

// getPostFromSearchResult: use Query result to get post
// params: searchResult -> searchResult of query in ES
// returns an array of Post
func getPostFromSearchResult(searchResult *elastic.SearchResult) []Post {
	var posts []Post
	var ptype Post
	for _, item := range searchResult.Each(reflect.TypeOf(ptype)) {
		p := item.(Post)
		posts = append(posts, p)
	}
	return posts
}

/*
 * connect go server with GCS
 */

// saveToGCS: save media file and return its URL in GCS
// params: r -> the file user uploaded, objectName -> object name in GCS the file will store in
// returns string -> url of the file in the GCS
func saveToGCS(r io.Reader, objectName string) (string, error) {
	// any http request to GCS need a parameter ctx
	ctx := context.Background()

	// create connection to GCS
	client, err := storage.NewClient(ctx)
	if err != nil {
		return "", err
	}

	// create a bucket instance, do not need to create new bucket because I manually created it
	bucket := client.Bucket(BUCKET_NAME)

	// check if bucket exists
	if _, err := bucket.Attrs(ctx); err != nil {
		return "", err
	}

	object := bucket.Object(objectName)
	wc := object.NewWriter(ctx)
	if _, err := io.Copy(wc, r); err != nil {
		return "", err
	}

	if err := wc.Close(); err != nil {
		return "", err
	}

	// set access control (ACL) of the object: all users can read
	if err := object.ACL().Set(ctx, storage.AllUsers, storage.RoleReader); err != nil {
		return "", err
	}

	attrs, err := object.Attrs(ctx)
	if err != nil {
		return "", err
	}

	fmt.Printf("Image is saved to GCS: %s\n", attrs.MediaLink)
	return attrs.MediaLink, nil
}
