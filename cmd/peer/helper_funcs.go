package main

import (
	"io/ioutil"
	"log"
	"strconv"
)

type FileData struct {
	Name string
	Data []byte
}

func ReadBinaryFiles(dir string, prefix string) ([]FileData, error) {

	var fileData []FileData

	for i := 0; i < 900; i++ {
		data, err := ioutil.ReadFile(dir + "/" + prefix + strconv.Itoa(i) + ".bin")
		if err != nil {
			log.Printf("Failed to read file %s: %v", prefix+strconv.Itoa(i)+".bin", err)
			continue
		}

		fileData = append(fileData, FileData{
			Name: prefix + strconv.Itoa(i) + ".bin",
			Data: data,
		})
	}

	return fileData, nil
}
