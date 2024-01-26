package server

import (
	"encoding/base64"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
	"log"
)

var PullServerCache = make(map[string]*PullServer)

// 创建一个 pull server, 是对等连接的服务端, 用于接收客户端的推流

// 创建一个WebRTC配置
var config = webrtc.Configuration{
	ICEServers: []webrtc.ICEServer{
		{URLs: []string{"stun:stun.l.google.com:19302"}},
	},
}

type PullServer struct {
	Id              string
	SignalConn      *websocket.Conn
	PeerConn        *webrtc.PeerConnection
	TrackChan       chan *webrtc.TrackLocalStaticRTP
	CloseChan       chan struct{}
	ClientMap       map[string]*PullServer
	LocalStaticRTPs []*webrtc.TrackLocalStaticRTP
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
				// panic(err)
			}
		}
	})

	p := &PullServer{
		Id:              channelId.String(),
		SignalConn:      c,
		PeerConn:        peerConnection,
		TrackChan:       make(chan *webrtc.TrackLocalStaticRTP),
		CloseChan:       make(chan struct{}),
		ClientMap:       make(map[string]*PullServer),
		LocalStaticRTPs: make([]*webrtc.TrackLocalStaticRTP, 0),
	}

	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		// log.Printf("Track has started, of type %d: %s \n", track.PayloadType(), track.Codec().MimeType)
		// 新增一个静态的本地 track
		localTrack, newTrackErr := webrtc.NewTrackLocalStaticRTP(track.Codec().RTPCodecCapability, track.ID(), track.StreamID())
		if newTrackErr != nil {
			panic(newTrackErr)
		}
		p.LocalStaticRTPs = append(p.LocalStaticRTPs, localTrack)
		for {
			// 从远端读取数据
			rtp, _, readErr := track.ReadRTP()
			if readErr != nil {
				panic(readErr)
			}
			// 将数据写入本地 track
			err := localTrack.WriteRTP(rtp)
			if err != nil {
				panic(err)
			}
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

func (p *PullServer) Join(push *PullServer) {
	p.ClientMap[push.Id] = push
	// 为新增的 push 创建 track 转发协程
	for _, track := range p.LocalStaticRTPs {
		rtpSender, err := push.PeerConn.AddTrack(track)
		if err != nil {
			panic(err)
		}
		go func(track *webrtc.TrackLocalStaticRTP, rtpSender *webrtc.RTPSender) {
			for {
				rtpSender.r
			}
		}(track, rtpSender)
	}
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
