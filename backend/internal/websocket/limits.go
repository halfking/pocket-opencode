package websocket

import "github.com/gorilla/websocket"

const maxWebSocketMessageBytes = 1 << 20

func setWebSocketReadLimit(conn *websocket.Conn) {
	conn.SetReadLimit(maxWebSocketMessageBytes)
}
