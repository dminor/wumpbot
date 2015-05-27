package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

type Config struct {
	Host string
	Nick string
	Chan string
}

func wumpReader(config Config, wumpout io.Reader, wumpCommands chan string, ircCommands chan string) {

	reader := bufio.NewReader(wumpout)

	var buffer bytes.Buffer

	buffer.WriteString(fmt.Sprintf("PRIVMSG #%v :", config.Chan))

	for {
		token, err := reader.ReadString(' ')

		//handle embedded newlines
		newline := strings.LastIndex(token, "\n")
		if newline != -1 {
			buffer.WriteString(token[:newline+1])
			ircCommands <- buffer.String()
			buffer.Reset()
			buffer.WriteString(fmt.Sprintf("PRIVMSG #%v :", config.Chan))
			token = token[newline+1:]
		}

		if err != nil {
			break
		}

		// handle instructions
		if token == "Instructions? " {
			for token != "(y-n) " {
				token, err = reader.ReadString(' ')
			}
			wumpCommands <- "n\n"
			continue
		}

		// suppress move or shoot
		if token == "Move " {
			for !strings.Contains(token, "(m-s)") {
				token, err = reader.ReadString(' ')
			}
			continue
		}

		// handle play again
		if token == "Care " {

			//play again prompt
			for !strings.Contains(token, "(y-n)") {
				token, err = reader.ReadString(' ')
			}
			wumpCommands <- "y\n"

			// same cave prompt
			token, err = reader.ReadString(' ')

			for !strings.Contains(token, "(y-n)") {
				token, err = reader.ReadString(' ')
			}
			wumpCommands <- "n\n"

			fmt.Println("Restarting game")
			continue
		}

		buffer.WriteString(token)
	}
}

func commandWriter(in io.Writer, commands chan string) {

	writer := bufio.NewWriter(in)

	for {
		command := <-commands
		writer.WriteString(command)
		writer.Flush()
	}
}

func ircReader(config Config, wumpout io.Reader, conn io.Reader, ircCommands chan string, wumpCommands chan string) {

	reader := bufio.NewReader(conn)

	state := 0

	cmdRexp := regexp.MustCompile(`[,:]? (m|s) (\d\d?)`)

	for {

		line, err := reader.ReadString('\n')

		if err != nil {
			break
		}

		// handle pings
		if strings.HasPrefix(line, "PING") {
			reply := line[6:]
			ircCommands <- "PONG " + reply + "\r\n"
			continue
		}

		// handle join
		switch state {
		case 0:
			ircCommands <- fmt.Sprintf("NICK %v\r\n", config.Nick)
			state++
		case 1:
			ircCommands <- fmt.Sprintf("USER %v wump 0 :%v\r\n",
				config.Nick, config.Nick)
			state++
		case 2:
			ircCommands <- fmt.Sprintf("JOIN #%v\r\n", config.Chan)
			state++
		case 3:
			go wumpReader(config, wumpout, wumpCommands, ircCommands)
			state++
		}

		// handle messages directed at us
		privmsg := fmt.Sprintf("PRIVMSG #%v :%v:", config.Chan, config.Nick)
		index := strings.Index(line, privmsg)
		if index != -1 {
			msg := line[index+len(privmsg):]

			// sanitize input to wump
			matches := cmdRexp.FindStringSubmatch(msg)
			if len(matches) == 3 {
				wumpCommands <- matches[1] + " " + matches[2] + "\n"
			} else {
				wumpCommands <- "wtf\n"
			}

			continue
		}

		fmt.Printf("%v", line)
	}
}

func main() {

	data, err := ioutil.ReadFile("config.json")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	var config Config
	err = json.Unmarshal(data, &config)

	cmd := exec.Command("wump")

	wumpout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	wumpin, err := cmd.StdinPipe()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	err = cmd.Start()
	if err != nil {
		fmt.Println(err)
		fmt.Println("Could not start wump command")
		os.Exit(1)
	}

	conn, err := net.Dial("tcp", config.Host)

	if err != nil {
		fmt.Println("Could not connect to IRC server: %v", config.Host)
		os.Exit(1)
	}

	wumpCommands := make(chan string)
	ircCommands := make(chan string)

	go commandWriter(wumpin, wumpCommands)
	go ircReader(config, wumpout, conn, ircCommands, wumpCommands)
	go commandWriter(conn, ircCommands)

	input := bufio.NewReader(os.Stdin)
	for {
		line, err := input.ReadString('\n')

		if err != nil {
			break
		}

		// command line quit - quit both wump and irc
		if line == "q\n" {
			wumpCommands <- "q\n"
			ircCommands <- "QUIT\r\n"
			break
		}
	}

	cmd.Wait()
}
