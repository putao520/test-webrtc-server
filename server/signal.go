package server

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
)

var addr = flag.String("addr", "127.0.0.1:3000", "http service address")

type BindMessage struct {
	ChannelId string `json:"channel_id"`
	Role      string `json:"role"`
	Type      string `json:"type"`
	Payload   string `json:"payload"` // base64 可选项
}

func NewSignalConnection(id string, peerConnection *webrtc.PeerConnection, role string, closeCallback func(string)) *websocket.Conn {
	// 注册信令通道
	u := url.URL{Scheme: "ws", Host: *addr, Path: "/"}
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	done := make(chan struct{})
	go func() {
		defer func() {
			closeCallback(id)
			close(done)
		}()
		for {
			_, message, err := c.ReadMessage()
			if err != nil {
				log.Println("read:", err)
				return
			}
			// log.Printf("recv: %s", message)
			cmd := BindMessage{}
			err = json.Unmarshal(message, &cmd)
			if err != nil {
				panic(err)
			}

			switch cmd.Type {
			case "offer":
				handleOffer(id, peerConnection, c, cmd.Payload, role)
			case "answer":
				handleAnswer(peerConnection, cmd.Payload)
			case "candidate":
				handleCandidate(id, peerConnection, c, cmd.Payload, role)
			default:
				println("unknown message type")
			}
		}
	}()

	return c
}

func handleOffer(id string, peerConnection *webrtc.PeerConnection, signalConnection *websocket.Conn, offerBase64 string, role string) {
	// base64 解码成 []byte
	sdpBytes, err := base64.StdEncoding.DecodeString(offerBase64)
	if err != nil {
		fmt.Println("Error decoding Base64 string:", err)
		return
	}
	sdp := webrtc.SessionDescription{}
	// 解码 base64 成 sdp
	if sdpErr := json.Unmarshal(sdpBytes, &sdp); sdpErr != nil {
		panic(sdpErr)
	}

	if sdpErr := peerConnection.SetRemoteDescription(sdp); sdpErr != nil {
		panic(sdpErr)
	}

	// 创建 answer
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		panic(err)
	}
	// 设置本地描述
	if err = peerConnection.SetLocalDescription(answer); err != nil {
		panic(err)
	}
	// 返回 answer
	answerBytes, err := json.Marshal(answer)
	if err != nil {
		panic(err)
	}
	answerBase64 := base64.StdEncoding.EncodeToString(answerBytes)
	answerMessage := BindMessage{
		ChannelId: id,
		Role:      role,
		Type:      "answer",
		Payload:   answerBase64,
	}
	err = signalConnection.WriteJSON(answerMessage)
	if err != nil {
		panic(err)
	}
}

func handleAnswer(peerConnection *webrtc.PeerConnection, answerBase64 string) {
	// base64 解码成 []byte
	sdpBytes, err := base64.StdEncoding.DecodeString(answerBase64)
	if err != nil {
		fmt.Println("Error decoding Base64 string:", err)
		return
	}
	sdp := webrtc.SessionDescription{}
	// 解码 base64 成 sdp
	if sdpErr := json.Unmarshal(sdpBytes, &sdp); sdpErr != nil {
		panic(sdpErr)
	}

	if sdpErr := peerConnection.SetRemoteDescription(sdp); sdpErr != nil {
		panic(sdpErr)
	}
}

func handleCandidate(id string, peerConnection *webrtc.PeerConnection, signalConnection *websocket.Conn, candidateBase64 string, role string) {
	// base64 解码成 []byte
	candidateBytes, err := base64.StdEncoding.DecodeString(candidateBase64)
	if err != nil {
		fmt.Println("Error decoding Base64 string:", err)
		return
	}
	candidate := webrtc.ICECandidateInit{}
	// 解码 base64 成 sdp
	if sdpErr := json.Unmarshal(candidateBytes, &candidate); sdpErr != nil {
		panic(sdpErr)
	}
	if err = peerConnection.AddICECandidate(candidate); err != nil {
		panic(err)
	}

	// 发送 candidate
	candidateMessage := BindMessage{
		ChannelId: id,
		Role:      role,
		Type:      "candidate",
		Payload:   candidateBase64,
	}
	err = signalConnection.WriteJSON(candidateMessage)
	if err != nil {
		panic(err)
	}
}
