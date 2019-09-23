package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strings"

	"golang.org/x/oauth2/google"
)

type Prediction struct {
	Prediction int       `json:"prediction"`
	Key        string    `json:"key"`
	Scores     []float64 `json:"scores"`
}

type MLResponseBody struct {
	Predictions []Prediction `json:"predictions"`
}

type ImageBytes struct {
	B64 []byte `json:"b64"`
}

type Instance struct {
	ImageBytes ImageBytes `json:"image_bytes"`
	Key        string     `json:"key"`
}

type MLRequestBody struct {
	Instances []Instance `json:"instances"`
}

const (
	// Replace this project ID and model name with your configuration.
	PROJECT = "around-179500"
	MODEL   = "face_face"
	URL     = "https://ml.googleapis.com/v1/projects/" + PROJECT + "/models/" + MODEL + ":predict"
	SCOPE   = "https://www.googleapis.com/auth/cloud-platform"
)
