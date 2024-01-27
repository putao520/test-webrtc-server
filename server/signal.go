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
	go func() {
		defer closeCallback(id)
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
			case "created":
				log.Printf("created: %s", message)
			default:
				println("unknown message type")
				log.Printf("recv: %s", message)
			}
		}
	}()

	// 处理 c close事件

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
		log.Println("unmarshal sdp error:", sdpErr)
		panic(sdpErr)
	}

	if sdpErr := peerConnection.SetRemoteDescription(sdp); sdpErr != nil {
		log.Println("set remote description error:", sdpErr)
		panic(sdpErr)
	}

	// 创建 answer
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		log.Println("create answer error:", err)
		panic(err)
	}
	// 设置本地描述
	if err = peerConnection.SetLocalDescription(answer); err != nil {
		log.Println("set local description error:", err)
		panic(err)
	}
	// 返回 answer
	answerBytes, err := json.Marshal(answer)
	if err != nil {
		log.Println("marshal answer error:", err)
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
		log.Println("write answer error:", err)
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
	log.Printf("send: ice")
}

func CreateOffer(id string, peerConnection *webrtc.PeerConnection, signalConnection *websocket.Conn, role string) {
	// 判断当前 peerConnection 是否需要创建 offer
	if peerConnection.RemoteDescription() != nil {
		return
	}
	// 创建 offer
	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		panic(err)
	}
	// 设置本地描述
	if err = peerConnection.SetLocalDescription(offer); err != nil {
		panic(err)
	}
	// 返回 offer
	offerBytes, err := json.Marshal(offer)
	if err != nil {
		panic(err)
	}
	offerBase64 := base64.StdEncoding.EncodeToString(offerBytes)
	offerMessage := BindMessage{
		ChannelId: id,
		Role:      role,
		Type:      "offer",
		Payload:   offerBase64,
	}
	err = signalConnection.WriteJSON(offerMessage)
	if err != nil {
		panic(err)
	}
}
