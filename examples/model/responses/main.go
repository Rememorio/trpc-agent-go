//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates direct streaming use of the OpenAI Responses API.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"trpc.group/trpc-go/trpc-agent-go/model"
	openairesponses "trpc.group/trpc-go/trpc-agent-go/model/openai/responses"
)

func main() {
	modelName := flag.String("model", "gpt-5.2", "OpenAI model name")
	flag.Parse()

	llm := openairesponses.New(*modelName)
	request := &model.Request{
		Messages: []model.Message{
			model.NewDeveloperMessage("Answer clearly and concisely."),
			model.NewUserMessage("Why does Go make concurrency a language-level concept?"),
		},
		GenerationConfig: model.GenerationConfig{Stream: true},
	}

	responseChannel, err := llm.GenerateContent(context.Background(), request)
	if err != nil {
		log.Fatal(err)
	}
	for response := range responseChannel {
		if response.Error != nil {
			log.Fatal(response.Error)
		}
		if len(response.Choices) == 0 {
			continue
		}
		choice := response.Choices[0]
		fmt.Print(choice.Delta.Content)
		if !response.Done {
			continue
		}
		metadata, ok := openairesponses.MetadataFromResponse(response)
		if ok {
			fmt.Printf("\n\nresponse_id=%s status=%s\n", metadata.ResponseID, metadata.Status)
		}
	}
}
