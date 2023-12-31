package main

import (
	"context"
	"fmt"
	"job-application-api/config"
	"job-application-api/internal/auth"
	"job-application-api/internal/caching"
	"job-application-api/internal/database"
	redisdb "job-application-api/internal/database/redis"
	"job-application-api/internal/handlers"
	"job-application-api/internal/repository"
	"job-application-api/internal/service"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog/log"
)

func main() {
	err := StartApplication()
	if err != nil {
		log.Panic().Err(err).Send()
	}
	log.Info().Msg("Welcome to Job Portal")
}
func StartApplication() error {
	cfg := config.GetConfig()
	log.Info().Msg("Main: Started: Intilaizing authentication")
	// privatePEM, err := os.ReadFile("private.pem")
	// if err != nil {
	// 	return fmt.Errorf("reading the auth private key %w", err)
	// }

	privatePEM := []byte(cfg.AuthConfig.PrivateKey)

	privateKey, err := jwt.ParseRSAPrivateKeyFromPEM(privatePEM)
	if err != nil {
		return fmt.Errorf("parsing private key %w", err)
	}
	// publicPEM, err := os.ReadFile("pubkey.pem")
	// if err != nil {
	// 	return fmt.Errorf("reading the auth public key %w", err)
	// }
	publicPEM := []byte(cfg.AuthConfig.PublicKey)
	publicKey, err := jwt.ParseRSAPublicKeyFromPEM(publicPEM)
	if err != nil {
		return fmt.Errorf("parsing public key %w", err)
	}
	a, err := auth.NewAuth(privateKey, publicKey)
	if err != nil {
		return fmt.Errorf("constructing auth %w", err)
	}
	log.Info().Msg("main : Started : Initializing db")
	db, err := database.Open(cfg.DataConfig)
	if err != nil {
		return fmt.Errorf("connecting to db %w", err)
	}
	pg, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get database instance: %w ", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	err = pg.PingContext(ctx)
	if err != nil {
		return fmt.Errorf("database is not connected: %w ", err)
	}

	client := redisdb.InitRedisClient(cfg.RedisConfig)

	cache := caching.NewRdbCache(client)
	repo, err := repository.NewRepository(db)
	if err != nil {
		return err
	}

	sc, err := service.NewService(repo, a, cache)
	if err != nil {
		return err
	}
	api := http.Server{
		Addr:         fmt.Sprintf("%s:%s", cfg.AppConfig.AppHost, cfg.AppConfig.AppPort),
		ReadTimeout:  time.Duration(cfg.AppConfig.AppReadTimeout) * time.Second,
		WriteTimeout: time.Duration(cfg.AppConfig.AppWriteTimeout) * time.Second,
		IdleTimeout:  time.Duration(cfg.AppConfig.AppIdleTimeout) * time.Second,
		Handler:      handlers.API(a, sc),
	}

	serverError := make(chan error, 1)
	go func() {
		log.Info().Str("port", api.Addr).Msg("main: API listening")
		serverError <- api.ListenAndServe()
	}()
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt)
	select {
	case err := <-serverError:
		return fmt.Errorf("server error %w", err)
	case sig := <-shutdown:
		log.Info().Msgf("main: Start shutdown %s", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := api.Shutdown(ctx)
		if err != nil {
			err := api.Close()
			return fmt.Errorf("could not stop server gracefully %w", err)
		}
	}
	return nil
}
