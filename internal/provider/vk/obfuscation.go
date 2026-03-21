package vk

import "math/rand/v2"

var joinParamSets = []string{
	"platform=WEB&appVersion=2.8.9&version=5&device=browser&clientType=SDK_JS&deviceIdx=0",
	"platform=WEB&appVersion=2.8.7&version=5&device=browser&clientType=SDK_JS&deviceIdx=0",
	"platform=WEB&appVersion=2.7.4&version=5&device=browser&clientType=SDK_WEB&deviceIdx=0",
}

var userAgentPool = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
}

func randomJoinParams() string {
	return joinParamSets[rand.IntN(len(joinParamSets))]
}

func randomUserAgent() string {
	return userAgentPool[rand.IntN(len(userAgentPool))]
}
