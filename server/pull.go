package server

import (
	"encoding/base64"
	"encoding/json"
	"log"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
)

var PullServerCache = make(map[string]*PullServer)

// 创建一个 pull server, 是对等连接的服务端, 用于接收客户端的推流

// 创建一个WebRTC配置
var config = webrtc.Configuration{
	ICEServers: []webrtc.ICEServer{
		{URLs: []string{"stun:stun.l.google.com:19302"}},
	},
}

type trackInfo struct {
	trackChan chan []byte
	rtpSender *webrtc.RTPSender
}

type PullServer struct {
	Id         string
	SignalConn *websocket.Conn
	PeerConn   *webrtc.PeerConnection
	trackChan  map[string]*trackInfo // 用于接收客户端的 track 数据

	CloseChan chan struct{}
	ClientMap map[string]*PullServer // 订阅的客户端对象
}

func NewPullServer() *PullServer {
	channelId, err := uuid.NewUUID()
	if err != nil {
		panic(err)
	}

	peerConnection, err := webrtc.NewPeerConnection(config)
	if err != nil {
		panic(err)
	}

	c := NewSignalConnection(channelId.String(), peerConnection, "server", func(id string) {
		DeletePullServer(id)
	})

	peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			candidateBytes, err := json.Marshal(candidate.ToJSON())
			if err != nil {
				panic(err)
			}
			candidateBase64 := base64.StdEncoding.EncodeToString(candidateBytes)
			candidateMessage := BindMessage{
				ChannelId: channelId.String(),
				Role:      "server",
				Type:      "candidate",
				Payload:   candidateBase64,
			}
			err = c.WriteJSON(candidateMessage)
			if err != nil {
				println(err)
			}
		}
	})

	p := &PullServer{
		Id:         channelId.String(),
		SignalConn: c,
		PeerConn:   peerConnection,
		trackChan:  make(map[string]*trackInfo),
		CloseChan:  make(chan struct{}),
		ClientMap:  make(map[string]*PullServer),
	}

	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		for _, client := range p.ClientMap {
			client.addRemoteTrack(track)
		}

		byteBuffer := make([]byte, 1400)
		for {
			// 从 track 中读取数据
			i, _, err := track.Read(byteBuffer)
			if err != nil {
				break
			}
			// 将数据写到每一个 client 的 trackChan 中
			for _, client := range p.ClientMap {
				trackInfo, err := client.trackChan[track.ID()]
				if !err {
					trackInfo.trackChan <- byteBuffer[:i]
				}
			}
		}

		// 从所有客户端中删除对应id的track并关闭对应的chan
		for _, client := range p.ClientMap {
			client.removeRemoteTrack(track.ID())
		}
	})

	PullServerCache[channelId.String()] = p

	p.Create()
	return p
}

func DeletePullServer(channelId string) {
	p := PullServerCache[channelId]
	if p == nil {
		return
	}
	for _, client := range p.ClientMap {
		client.Close()
	}
	p.Close()
	delete(PullServerCache, channelId)
}

func (p *PullServer) addRemoteTrack(track *webrtc.TrackRemote) *webrtc.TrackLocalStaticRTP {
	newTrack, newTrackErr := webrtc.NewTrackLocalStaticRTP(track.Codec().RTPCodecCapability, track.ID(), track.StreamID())
	if newTrackErr != nil {
		panic(newTrackErr)
	}
	rtpSender, err := p.PeerConn.AddTrack(newTrack)
	if err != nil {
		panic(err)
	}
	// 创建接收 track 数据的通道
	trackChan := make(chan []byte)

	trackInfo := &trackInfo{
		trackChan: trackChan,
		rtpSender: rtpSender,
	}

	p.trackChan[track.ID()] = trackInfo
	// 创建转发协程
	go func() {
		for {
			data, err := <-trackChan
			if !err {
				break
			}
			newTrack.Write(data)
		}
	}()
	return newTrack
}

func (p *PullServer) removeRemoteTrack(trackId string) {
	trackInfo, err := p.trackChan[trackId]
	if !err {
		return
	}
	delete(p.trackChan, trackId)
	close(trackInfo.trackChan)
	p.PeerConn.RemoveTrack(trackInfo.rtpSender)
}

func (p *PullServer) Join(push *PullServer) {
	p.ClientMap[push.Id] = push
}

func (p *PullServer) Leave(id string) {
	cli := p.ClientMap[id]
	if cli == nil {
		return
	}
	for trackId, _ := range cli.trackChan {
		cli.removeRemoteTrack(trackId)
	}
	delete(p.ClientMap, id)
}

func (p *PullServer) Create() {
	// 发送加入房间消息
	createMessage := BindMessage{
		ChannelId: p.Id,
		Role:      "server",
		Type:      "create",
	}
	err := p.SignalConn.WriteJSON(createMessage)
	if err != nil {
		log.Println("write:", err)
	}
}

func (p *PullServer) Close() {
	p.SignalConn.Close()
	p.PeerConn.Close()
	p.CloseChan <- struct{}{}
	// 所有包含该频道的客户端的转发通道都关闭
}
