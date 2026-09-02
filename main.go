package main

import (
	"context"
	"log"
	"os"
	"time"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("manapool-jobs: ")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	st, err := newStore(ctx, postgresURL())
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer st.Close()
	if err := st.migrate(ctx); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	res, err := run(ctx, Config{
		JobsURL:      envStr("JOBS_URL", "https://manapool.com/jobs"),
		LLMURL:       envStr("LLM_URL", "http://llm.llm.svc.cluster.local"),
		LLMModel:     envStr("LLM_MODEL", "gpt-4o-mini"),
		DiscordURL:   envStr("DISCORD_URL", "http://discord.discord.svc.cluster.local"),
		DiscordRoute: envStr("DISCORD_ROUTE", "default"),
		HTTP:         defaultHTTP(),
		Store:        st,
	})
	if err != nil {
		log.Fatalf("tick: %v", err)
	}
	log.Printf("seen=%d new=%d reopened=%d alerted=%d bootstrap=%v",
		res.Seen, res.New, res.Reopened, res.Alerted, res.Bootstrap)
	os.Exit(0)
}
