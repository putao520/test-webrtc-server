package main

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"net/http"
	"server/server"
)

func main() {
	r := gin.New()
	corsConf := cors.DefaultConfig()
	corsConf.AddAllowHeaders("Authorization")
	corsConf.AllowAllOrigins = true
	r.Use(gin.Recovery())
	r.Use(cors.New(corsConf))
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "pong",
		})
	})

	// 创建一个上传绑定
	r.GET("/create", func(c *gin.Context) {
		p := server.NewPullServer()
		c.JSON(http.StatusOK, gin.H{
			"code":    0,
			"message": "success",
			"data": gin.H{
				"channel_id": p.Id,
			},
		})
	})

	// 加入一个下载绑定连接
	r.GET("/join", func(c *gin.Context) {
		// 获得channel_id
		channelId := c.Query("channel_id")
		p := server.PullServerCache[channelId]
		if p == nil {
			c.JSON(http.StatusOK, gin.H{
				"code":    1,
				"message": "频道不存在",
			})
			return
		}
		userId := c.Query("user_id")
		v := server.PullServerCache[userId]
		if v == nil {
			c.JSON(http.StatusOK, gin.H{
				"code":    1,
				"message": "用户不存在",
			})
			return
		}
		// 订阅流
		p.Join(v)
		c.JSON(http.StatusOK, gin.H{
			"code":    0,
			"message": "success",
		})
	})

	r.Run() // listen and serve on 0.0.0.0:8080 (for windows "localhost:8080")
}
