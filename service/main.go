package main

import (
	"cloud.google.com/go/bigtable"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"reflect"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/olivere/elastic"
	"github.com/pborman/uuid"

	"cloud.google.com/go/storage"
)

type Location struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type Post struct {
	// `json:"user"` is for the json parsing of this User field. Otherwise, it's 'User' by default
	User     string   `json:"user"`
	Message  string   `json:"message"`
	Location Location `json:"location"`
	Url      string   `json:"url"`
}

const (
	POST_INDEX           = "post"
	POST_TYPE            = "post"
	DISTANCE             = "200km"
	ES_URL               = "http://35.193.104.85:9200"
	BUCKET_NAME          = "yewenlyu-project-radius"
	PROJECT_ID           = "radius-252718"
	BIGTABLE_INSTANCE_ID = "radius-post"
)

func main() {
	fmt.Println("started-service")
	createIndexIfNotExist()

	r := mux.NewRouter()

	r.Handle("/post", http.HandlerFunc(handlerPost)).Methods("POST", "OPTIONS")
	r.Handle("/search", http.HandlerFunc(handlerSearch)).Methods("GET", "OPTIONS")

	http.Handle("/", r)

	log.Fatal(http.ListenAndServe(":8080", nil))

}

// This function sets up an ElasticSearch client, prepares the mapping for the indices
func createIndexIfNotExist() {

	// create a ElasticSearch client, no sniff for now
	client, err := elastic.NewClient(elastic.SetURL(ES_URL), elastic.SetSniff(false))
	if err != nil {
		panic(err)
	}

	exists, err := client.IndexExists(POST_INDEX).Do(context.Background())
	if err != nil {
		panic(err)
	}

	// create a mapping for the index
	if !exists {
		mapping := `{
            "mappings": {
                "post": {
                    "properties": {
                        "location": {
                            "type": "geo_point"
                        }
                    }
                }
            }
        }`
		_, err = client.CreateIndex(POST_INDEX).Body(mapping).Do(context.Background())
		if err != nil {
			panic(err)
		}
	}
}

func handlerPost(w http.ResponseWriter, r *http.Request) {

	// parse from body of request to get a json object.
	fmt.Println("Received one post request")

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization")

	if r.Method == "OPTIONS" {
		return
	}

	lat, _ := strconv.ParseFloat(r.FormValue("lat"), 64)
	lon, _ := strconv.ParseFloat(r.FormValue("lon"), 64)

	p := &Post{
		User:    r.FormValue("user"),
		Message: r.FormValue("message"),
		Location: Location{
			Lat: lat,
			Lon: lon,
		},
	}

	id := uuid.New()
	file, _, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "Image is not available", http.StatusBadRequest)
		fmt.Printf("Image is not available %v.\n", err)
		return
	}
	attrs, err := saveToGCS(file, BUCKET_NAME, id)
	if err != nil {
		http.Error(w, "Failed to save image to GCS", http.StatusInternalServerError)
		fmt.Printf("Failed to save image to GCS %v.\n", err)
		return
	}
	p.Url = attrs.MediaLink

	err = saveToES(p, id)
	if err != nil {
		http.Error(w, "Failed to save post to ElasticSearch", http.StatusInternalServerError)
		fmt.Printf("Failed to save post to ElasticSearch %v.\n", err)
		return
	}
	fmt.Printf("Saved one post to ElasticSearch: %s", p.Message)

	err = saveToBigTable(p, id)
	if err != nil {
		http.Error(w, "Failed to save post to BigTable", http.StatusInternalServerError)
		fmt.Printf("Failed to save post to BigTable %v.\n", err)
		return
	}
}

func handlerSearch(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Received one request for search")

	// allow cross domain visit for JavaScript
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization")

	lat, _ := strconv.ParseFloat(r.URL.Query().Get("lat"), 64)
	lon, _ := strconv.ParseFloat(r.URL.Query().Get("lon"), 64)
	ran := DISTANCE // range is optional
	if val := r.URL.Query().Get("range"); val != "" {
		ran = val + "km"
	}

	fmt.Println("range is ", ran)

	// read data from ElasticSearch
	posts, err := readFromES(lat, lon, ran)
	if err != nil {
		http.Error(w, "Failed to read post from ElasticSearch", http.StatusInternalServerError)
		fmt.Printf("Failed to read post from ElasticSearch %v.\n", err)
		return
	}

	js, err := json.Marshal(posts)
	if err != nil {
		http.Error(w, "Failed to parse posts into JSON format", http.StatusInternalServerError)
		fmt.Printf("Failed to parse posts into JSON format %v.\n", err)
		return
	}

	w.Write(js)
}

// This function saves a post to ElasticSearch
func saveToES(post *Post, id string) error {
	client, err := elastic.NewClient(elastic.SetURL(ES_URL), elastic.SetSniff(false))
	if err != nil {
		return err
	}

	_, err = client.Index().
		Index(POST_INDEX).
		Type(POST_TYPE).
		Id(id).
		BodyJson(post).
		Refresh("wait_for").
		Do(context.Background())
	if err != nil {
		return err
	}

	fmt.Printf("Post is saved to index: %s\n", post.Message)
	return nil
}

// This function implements the GeoDistanceQuery logic
func readFromES(lat, lon float64, ran string) ([]Post, error) {

	// create a connection to ES, return if err occurs
	client, err := elastic.NewClient(elastic.SetURL(ES_URL), elastic.SetSniff(false))
	if err != nil {
		return nil, err
	}

	// prepare a geo-based query to find posts within a geological box
	query := elastic.NewGeoDistanceQuery("location")
	query = query.Distance(ran).Lat(lat).Lon(lon)

	// get the result based on index and query, format the output
	searchResult, err := client.Search().
		Index(POST_INDEX).
		Query(query).
		Pretty(true).
		Do(context.Background())
	if err != nil {
		return nil, err
	}

	// searchResult is of type SearchResult and returns hits, suggestions,
	// and all kinds of other information from Elasticsearch.
	fmt.Printf("Query took %d milliseconds\n", searchResult.TookInMillis)

	// Each one is a convenience function that iterates over hits in a search result.
	// It makes sure you don't need to check for nil values in the response.
	// However, it ignores errors in serialization.
	var ptyp Post
	var posts []Post

	// cast searchResults to type Post
	for _, item := range searchResult.Each(reflect.TypeOf(ptyp)) {
		if p, ok := item.(Post); ok {
			posts = append(posts, p)
		}
	}

	return posts, nil
}

func saveToGCS(r io.Reader, bucketName, objectName string) (*storage.ObjectAttrs, error) {

	ctx := context.Background()

	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}

	bucket := client.Bucket(bucketName)
	if _, err := bucket.Attrs(ctx); err != nil {
		return nil, err
	}

	object := bucket.Object(objectName)
	wc := object.NewWriter(ctx)
	if _, err = io.Copy(wc, r); err != nil {
		return nil, err
	}
	if err := wc.Close(); err != nil {
		return nil, err
	}

	if err = object.ACL().Set(ctx, storage.AllUsers, storage.RoleReader); err != nil {
		return nil, err
	}

	attrs, err := object.Attrs(ctx)
	if err != nil {
		return nil, err
	}

	fmt.Printf("Image is saved to GCS: %s\n", attrs.MediaLink)
	return attrs, nil
}

func saveToBigTable(p *Post, id string) error {
	ctx := context.Background()
	bt_client, err := bigtable.NewClient(ctx, PROJECT_ID, BIGTABLE_INSTANCE_ID)
	if err != nil {
		return err
	}

	tbl := bt_client.Open("post")
	mut := bigtable.NewMutation()
	t := bigtable.Now()
	mut.Set("post", "user", t, []byte(p.User))
	mut.Set("post", "message", t, []byte(p.Message))
	mut.Set("location", "lat", t, []byte(strconv.FormatFloat(p.Location.Lat, 'f', -1, 64)))
	mut.Set("location", "lon", t, []byte(strconv.FormatFloat(p.Location.Lon, 'f', -1, 64)))

	err = tbl.Apply(ctx, id, mut)
	if err != nil {
		return err
	}
	fmt.Printf("Post is saved to BigTable: %s\n", p.Message)
	return nil
}
