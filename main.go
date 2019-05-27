package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/bradleyfalzon/ghinstallation"
	"github.com/dgrijalva/jwt-go"
	"github.com/dimfeld/httptreemux"
	"github.com/go-redis/redis"
	"github.com/google/go-github/v25/github"
	"github.com/joho/godotenv"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func createToken() (string, error) {
	claim := &jwt.StandardClaims{
		IssuedAt:  time.Now().Unix(),
		ExpiresAt: time.Now().Add(time.Minute * 10).Unix(),
		Issuer:    os.Getenv("GITHUB_APP_IDENTIFIER"),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claim)

	ss, err := token.SignedString(os.Getenv("GITHUB_PRIVATE_KEY"))
	return ss, err
}

type ReminderEvent struct {
	InstallationID int64  `json:"installationId"`
	IssueNumber    int    `json:"issueNumber"`
	CommentID      int64  `json:"commentId"`
	RepoOwner      string `json:"repoOwner"`
	RepoName       string `json:"repoName"`
	CommentAuthor  string `json:"commentAuthor"`
}

func (event ReminderEvent) SendReminderComment() error {
	tr := http.DefaultTransport
	appId, err := strconv.Atoi(os.Getenv("GITHUB_APP_IDENTIFIER"))
	if err != nil {
		panic(err)
	}
	itr, err := ghinstallation.NewKeyFromFile(tr, appId, int(event.InstallationID), "mjeeves.2019-05-26.private-key.pem")
	if err != nil {
		panic(err)
	}

	client := github.NewClient(&http.Client{Transport: itr})
	commentToWrite := fmt.Sprintf("Don't forget about this issue @%s!", event.CommentAuthor)

	comment, _, err := client.Issues.CreateComment(context.Background(), event.RepoOwner, event.RepoName, event.IssueNumber, &github.IssueComment{
		Body: &commentToWrite,
	})

	fmt.Println(comment)
	return err
}

func runAPI() {

	remindMessageRe, err := regexp.Compile(`\/remind \d* (day|hour|minute)`)

	if err != nil {
		panic(err)
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr:     os.Getenv("REDIS_URL"),
		Password: "",
		DB:       0,
	})

	router := httptreemux.NewContextMux()

	router.GET("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "GET /")
	})

	router.POST("/event_handler", func(w http.ResponseWriter, r *http.Request) {
		eventType := r.Header.Get("X-Github-Event")
		fmt.Printf("Event Type: %s\n", eventType)
		installationEvent := github.IssueCommentEvent{}

		err := json.NewDecoder(r.Body).Decode(&installationEvent)
		if err != nil {
			panic(err)
		}

		fmt.Printf("Installing for %d\n", *(installationEvent.Installation.ID))
		tr := http.DefaultTransport
		appId, err := strconv.Atoi(os.Getenv("GITHUB_APP_IDENTIFIER"))
		if err != nil {
			panic(err)
		}
		itr, err := ghinstallation.NewKeyFromFile(tr, appId, int(*installationEvent.Installation.ID), "mjeeves.2019-05-26.private-key.pem")
		if err != nil {
			panic(err)
		}

		client := github.NewClient(&http.Client{Transport: itr})
		if eventType == "issue_comment" && *installationEvent.Action == "created" && strings.Contains(installationEvent.Comment.GetBody(), "/remind") {
			var score float64
			if remindMessageRe.MatchString(installationEvent.GetComment().GetBody()) {
				fmt.Printf("Message: %s matched\n", installationEvent.GetComment().GetBody())
				splitMessage := strings.Split(installationEvent.GetComment().GetBody(), " ")
				increment, err := strconv.Atoi(splitMessage[1])
				if err != nil {
					panic(err)
				}
				unit := strings.ToLower(splitMessage[2])
				var duration time.Duration
				if strings.Contains(unit, "day") {
					duration = time.Hour * 24 * time.Duration(increment)
				} else if strings.Contains(unit, "hour") {
					duration = time.Hour * time.Duration(increment)
				} else {
					duration = time.Minute * time.Duration(increment)
				}

				score = float64(time.Now().Add(duration).Unix())
			} else {
				score = float64(time.Now().Add(time.Minute * 10).Unix())
			}

			commentToWrite := "Alright I'll remind you!"
			_, _, err := client.Issues.CreateComment(context.Background(), installationEvent.Repo.GetOwner().GetLogin(), installationEvent.Repo.GetName(), installationEvent.Issue.GetNumber(), &github.IssueComment{
				Body: &commentToWrite,
			})
			if err != nil {
				panic(err)
			}

			event := ReminderEvent{
				InstallationID: installationEvent.Installation.GetID(),
				IssueNumber:    installationEvent.Issue.GetNumber(),
				CommentID:      installationEvent.Comment.GetID(),
				RepoOwner:      installationEvent.Repo.GetOwner().GetLogin(),
				RepoName:       installationEvent.Repo.GetName(),
				CommentAuthor:  installationEvent.Comment.GetUser().GetLogin(),
			}

			redisClient.ZAdd("scheduled_reminders", redis.Z{
				Score: score,
				Member: installationEvent.Comment.GetID(),
			})

			marshalledEvent, err := json.Marshal(event)
			if err != nil {
				panic(err)
			}

			key := strconv.Itoa(int(installationEvent.Comment.GetID()))

			err = redisClient.Set(key, marshalledEvent, 0).Err()

			if err != nil {
				panic(err)
			}
		}
		fmt.Fprintf(w, "POST /event_handler token: %s", "a")
	})

	fmt.Println("Listening on :3000")
	http.ListenAndServe(":3000", router)
}

func runWorker() {
	redisClient := redis.NewClient(&redis.Options{
		Addr:     os.Getenv("REDIS_URL"),
		Password: "",
		DB:       0,
	})

	for {
		maxTime := strconv.Itoa(int(time.Now().Unix()))

		vals, err := redisClient.ZRangeByScore("scheduled_reminders", redis.ZRangeBy{
			Min: "-inf",
			Max: maxTime,
		}).Result()

		if err != nil {
			panic(err)
		}

		for _, v := range vals {
			fmt.Println(v)
			event, err := redisClient.Get(v).Result()

			if err != nil {
				panic(err)
			}

			fmt.Println(event)
			parsedEvent := ReminderEvent{}

			err = json.Unmarshal([]byte(event), &parsedEvent)
			if err != nil {
				panic(err)
			}

			err = parsedEvent.SendReminderComment()
			if err != nil {
				panic(err)
			}

			err = redisClient.ZRem("scheduled_reminders", v).Err()

			if err != nil {
				panic(err)
			}

			err = redisClient.Del(v).Err()

			if err != nil {
				panic(err)
			}
		}

		fmt.Println("Finished Run, Sleeping")
		time.Sleep(time.Minute * 10)
	}
}

func main() {
	_ = godotenv.Load()

	runWorkers := flag.Bool("run-workers", false, "Whether to run workers or not")

	flag.Parse()

	if *runWorkers {
		fmt.Println("Running Workers")
		runWorker()
	} else {
		fmt.Println("Running API")
		runAPI()
	}

}
