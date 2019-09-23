package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"time"

	jwt "github.com/dgrijalva/jwt-go"
	"github.com/olivere/elastic"
)

const (
	USER_INDEX = "user"
	USER_TYPE  = "user"
)

type User struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Age      int64  `json:"age"`
	Gender   string `json:"gender"`
}

var mySigningKey = []byte("secret")

func checkUser(username, password string) error {
	client, err := elastic.NewClient(elastic.SetURL(ES_URL), elastic.SetSniff(false))
	if err != nil {
		return err
	}

	query := elastic.NewTermQuery("username", username)

	searchResult, err := client.Search().
		Index(USER_INDEX).
		Query(query).
		Pretty(true).
		Do(context.Background())
	if err != nil {
		return err
	}

	var utyp User
	for _, item := range searchResult.Each(reflect.TypeOf(utyp)) {
		if u, ok := item.(User); ok {
			if username == u.Username && password == u.Password {
				fmt.Printf("Login as %s\n", username)
				return nil
			}
		}
	}

	return errors.New("Wrong username or password")
}

func addUser(user User) error {
	client, err := elastic.NewClient(elastic.SetURL(ES_URL), elastic.SetSniff(false))
	if err != nil {
		return err
	}

	// select * from users where username = ?
	query := elastic.NewTermQuery("username", user.Username)

	searchResult, err := client.Search().
		Index(USER_INDEX).
		Query(query).
		Pretty(true).
		Do(context.Background())
	if err != nil {
		return err
	}

	if searchResult.TotalHits() > 0 {
		return errors.New("User already exists")
	}

	_, err = client.Index().
		Index(USER_INDEX).
		Type(USER_TYPE).
		Id(user.Username).
		BodyJson(user).
		Refresh("wait_for").
		Do(context.Background())
	if err != nil {
		return err
	}

	fmt.Printf("User is added: %s\n", user.Username)
	return nil
}
