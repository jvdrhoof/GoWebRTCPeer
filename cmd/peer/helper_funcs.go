package main

import (
	"io/ioutil"
	"log"
)

type FileData struct {
	Name string
	Data []byte
}

func ReadBinaryFiles(contentDirectory string) ([]FileData, error) {

	var fileData []FileData

	files, err := ioutil.ReadDir(contentDirectory)
	if err != nil {
		log.Fatal(err)
	}
	for _, f := range files {
		data, err := ioutil.ReadFile(contentDirectory + "/" + f.Name())
		if err != nil {
			log.Printf("Failed to read file %s", f.Name())
			return nil, err
		}

		fileData = append(fileData, FileData{
			Name: f.Name(),
			Data: data,
		})
	}

	return fileData, nil
}
