package main

import (
	"fmt"
	"log/slog"
	"os"
)

func main(){
	base := slog.NewJSONHandler(os.Stdout , &slog.HandlerOptions{ Level: slog.LevelInfo} )
	// logger := slog.New(tele)
	
	fmt.Println(base)
}