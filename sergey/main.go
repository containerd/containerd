package main

import (
	"fmt"
	"net/url"
)

func main() {
		strUrl:= "binary:/home/sergey/Studing/Final-Qualifying-Work/ContainerdUpdate/plugin?value1&value2"
		u, err := url.Parse(strUrl)
		if err != nil {
			fmt.Print(err.Error())
			return
		}
	fmt.Println("Raw = " + strUrl)

	fmt.Println("url to string = " +u.String())

}
