package main

import (
	"fmt"
	"net/url"
)

func main() {
		strUrl:= "binary:/home/sergey/Studing/Final-Qualifying-Work/ContainerdUpdate/plugin?/home/sergey/Studing/Final-Qualifying-Work/ContainerdUpdate/logs.txt&amqp://sergey:123@localhost:5672/&/home/sergey/Studing/Final-Qualifying-Work/ContainerdUpdate/backup.json&/home/sergey/Studing/Final-Qualifying-Work/ContainerdUpdate/pluginLogs.txt"
		u, err := url.Parse(strUrl)
		if err != nil {
			fmt.Print(err.Error())
			return
		}
	fmt.Println("Raw = " + strUrl)

	fmt.Println("url to string = " +u.String())

}
