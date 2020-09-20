/*
 * run index.go to create index(table) in ElasticSearch
 */
package main

import (
	"context"
	"fmt"

	"github.com/olivere/elastic"
)

const (
	POST_INDEX = "post"
	USER_INDEX = "user"

	// use YOUR_GCE_INTERNAL_IP_ADDRESS, because it won't change over time
	ES_URL = "http://10.128.0.2:9200"
)

func main() {
	// Create a new Client of ElasticSearch
	client, err := elastic.NewClient(elastic.SetURL(ES_URL))
	if err != nil {
		panic(err)
	}

	// check if the index(==database) called "post" exists
	exists, err := client.IndexExists(POST_INDEX).Do(context.Background()) // IndexExists is actually a http request to ElasticSearch, context means if the req has other parameter, for example, deadline / terminate early
	if err != nil {
		panic(err)
	}

	// create index if not exists
	if !exists {
		// mapping is schema
		mapping := `{
			"mappings": {
				"properties": {
					"user": {"type": "keyword", "index": false},
					"message": {"type": "keyword", "index": false},
					"location": {"type": "geo_point"},
					"url": {"type": "keyword", "index": false},
					"type": {"type": "keyword", "index": false},
					"face": {"type": "float"}
				}
			}
		}`
		_, err := client.CreateIndex(POST_INDEX).Body(mapping).Do(context.Background())
		if err != nil {
			panic(err)
		}
	}

	// Create user index
	exists, err = client.IndexExists(USER_INDEX).Do(context.Background())
	if err != nil {
		panic(err)
	}

	if !exists {
		mapping := `{
                        "mappings": {
                                "properties": {
                                        "username": {"type": "keyword"},
                                        "password": {"type": "keyword", "index": false},
                                        "age":      {"type": "long", "index": false},
                                        "gender":   {"type": "keyword", "index": false}
                                }
                        }
                }`
		_, err = client.CreateIndex(USER_INDEX).Body(mapping).Do(context.Background())
		if err != nil {
			panic(err)
		}
	}

	fmt.Println("Post index is created.")
}
