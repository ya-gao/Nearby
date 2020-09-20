/*
 * connect with Cloud Vision API
 */

package main

import (
	"context"
	"fmt"

	// import apiv1 as vision
	vision "cloud.google.com/go/vision/apiv1"
)

// annotate: Annotate an image file based on Cloud Vision API
// params: uri -> uri of object in GCS
// returns score and error if exists
func annotate(uri string) (float32, error) {
	ctx := context.Background()

	// create a client
	client, err := vision.NewImageAnnotatorClient(ctx)
	if err != nil {
		return 0.0, err
	}
	defer client.Close()

	// get image from GCS uri
	image := vision.NewImageFromURI(uri)

	// detect one face, annotation length should be 1
	annotations, err := client.DetectFaces(ctx, image, nil, 1)
	if err != nil {
		return 0.0, err
	}
	if len(annotations) == 0 {
		fmt.Println("No faces found.")
		return 0.0, nil
	}

	return annotations[0].DetectionConfidence, nil
}
