package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/gofrs/uuid"
	"github.com/gorilla/websocket"
)

var addr = flag.String("addr", "localhost:5000", "http service address") //when deploying to cloud, use instance private IP

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true //default true for testing purposes
	},
}

// Player : stores player connection
type Player struct {
	uid    string
	test   chan []byte
	push   chan map[string]interface{}
	ghub   *GameHub
	conn   *websocket.Conn
	radius int
	xPos   float64
	yPos   float64
}

// GameHub : stores all the players
type GameHub struct {
	activate   chan map[string]interface{}
	deactivate chan []byte
	publish    chan map[string]interface{}
	sync.Mutex
	Players map[*Player]bool
}

//create gamehub object
func initGameHub() *GameHub {
	return &GameHub{
		activate:   make(chan map[string]interface{}),
		deactivate: make(chan []byte),
		publish:    make(chan map[string]interface{}),
		Players:    make(map[*Player]bool),
	}
}

//distribute state to players
func (g *GameHub) conductor() {
	for {
		select {
		case message := <-g.publish:
			fmt.Printf("Publishing event %v\n", message)
			for player := range g.Players {
				if player.uid != message["id"] {
					fmt.Printf("to: %s\n", message["id"].(string))
					player.push <- message
				}
			}
		case message := <-g.activate:
			fmt.Printf("Activating new player with: %v\n", message)
			for player := range g.Players {
				if player.uid != message["uid"] {
					player.push <- message
				} else {
					var existingPlayer map[string]interface{}
					for k := range g.Players {
						if player.uid != k.uid {
							existingPlayer = map[string]interface{}{
								"name":    "new",
								"centerX": k.xPos,
								"centerY": k.yPos,
								"radius":  k.radius,
								"uid":     k.uid,
							}
							player.push <- existingPlayer
						}
					}
				}
			}
		}
	}
}

//Initialize Player and add it to current players map
func (g *GameHub) createPlayer(conn *websocket.Conn) *Player {
	p1 := uuid.NewV4()

	p := &Player{
		uid:    p1.String(),
		test:   make(chan []byte),
		push:   make(chan map[string]interface{}),
		ghub:   g,
		conn:   conn,
		radius: 20,
		xPos:   512,
		yPos:   512,
	}
	g.Lock()
	defer g.Unlock()
	g.Players[p] = true
	return p
}

//remove player object from current players map
//close channel associated with that player
func (g *GameHub) destroyPlayer(player *Player) {
	g.Lock()
	defer g.Unlock()
	if _, exist := g.Players[player]; exist {
		delete(g.Players, player)
		close(player.push)
		close(player.test)
		player = nil //clear pointer, garbage collector will free memory associated with player
		return
	}
}

//read messages coming in from websocket and pass it to messageNode
func (p *Player) inStream() {
	defer func() {
		p.ghub.destroyPlayer(p)
		p.conn.Close()
	}()
	for {
		var data map[string]interface{}
		err := p.conn.ReadJSON(&data)

		if err != nil {
			log.Println("err reading msg")
			break
		}

		//update player position in central
		p.xPos = data["positionX"].(float64)
		p.yPos = data["positionY"].(float64)

		p.ghub.publish <- data
	}
}

//send message out on player's websocket connection
func (p *Player) outStream() {
	defer func() {
		p.ghub.destroyPlayer(p)
		p.conn.Close()
	}()
	for {
		select {
		case msg := <-p.test:
			if err := p.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case m := <-p.push:
			fmt.Println("datasend")
			fmt.Println(m)
			if err := p.conn.WriteJSON(m); err != nil {
				return
			}
		}
	}
}

func spinConnection(ghub *GameHub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	player := ghub.createPlayer(conn)

	go player.inStream()
	go player.outStream()

	m := map[string]interface{}{
		"name": "getId",
		"uid":  player.uid,
	}
	data := map[string]interface{}{
		"name":    "new",
		"centerX": "512",
		"centerY": "512",
		"radius":  "25",
		"uid":     player.uid,
	}
	player.push <- m
	ghub.activate <- data
}

func main() {
	ghub := initGameHub()
	http.HandleFunc("/playersocket", func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("New Connection")
		spinConnection(ghub, w, r)
	})
	go ghub.conductor()
	err := http.ListenAndServe(*addr, nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
