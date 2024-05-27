package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-chi/chi/v5"
	"github.com/needsomesleeptd/annotater-app/src/config"
	logger_setup "github.com/needsomesleeptd/annotater-app/src/logger"
	nn_adapter "github.com/needsomesleeptd/annotater-core/NN/NNAdapter"
	nn_model_handler "github.com/needsomesleeptd/annotater-core/NN/NNAdapter/NNmodelhandler"
	report_creator "github.com/needsomesleeptd/annotater-core/reportCreatorService/reportCreator"
	"github.com/needsomesleeptd/annotater-core/service"
	models_da "github.com/needsomesleeptd/annotater-repository/modelsDA"
	repo_adapter "github.com/needsomesleeptd/annotater-repository/repositoryAdapter"
	"github.com/needsomesleeptd/annotater-repository/storage"
	auth_utils_adapter "github.com/needsomesleeptd/annotater-utils/pkg/authUtils"
	annot_handler "github.com/needsomesleeptd/http-server/http-server/handlers/annot"
	annot_type_handler "github.com/needsomesleeptd/http-server/http-server/handlers/annotType"
	auth_handler "github.com/needsomesleeptd/http-server/http-server/handlers/auth"
	document_handler "github.com/needsomesleeptd/http-server/http-server/handlers/document"
	user_handler "github.com/needsomesleeptd/http-server/http-server/handlers/user"
	"github.com/needsomesleeptd/http-server/middleware/access_middleware"
	"github.com/needsomesleeptd/http-server/middleware/auth_middleware"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// andrew1 2
// admin admin
// control control

func migrate(db *gorm.DB) error {
	err := db.AutoMigrate(&models_da.Document{})
	if err != nil {
		return err
	}
	err = db.AutoMigrate(&models_da.User{})
	if err != nil {
		return err
	}
	err = db.AutoMigrate(&models_da.MarkupType{})
	if err != nil {
		return err
	}
	err = db.AutoMigrate(&models_da.Markup{})
	if err != nil {
		return err
	}
	return nil
}

func main() {

	config := config.MustLoad()
	postgresConStr := config.Database.GetGormConnectStr()
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: postgresConStr}),
		&gorm.Config{TranslateError: true,
			Logger: logger.Default.LogMode(logger.Silent)})

	log := logger_setup.Setuplog(config)

	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
	err = migrate(db)
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}

	//auth service
	userRepo := repo_adapter.NewUserRepositoryAdapter(db)
	hasher := auth_utils_adapter.NewPasswordHashCrypto()
	tokenHandler := auth_utils_adapter.NewJWTTokenHandler()
	authService := service.NewAuthService(log, userRepo, hasher, tokenHandler, service.SECRET)

	//annot service
	annotRepo := repo_adapter.NewAnotattionRepositoryAdapter(db)
	annotService := service.NewAnnotattionService(log, annotRepo)

	//annotType service
	annotTypeRepo := repo_adapter.NewAnotattionTypeRepositoryAdapter(db)
	annotTypeService := service.NewAnotattionTypeService(log, annotTypeRepo)

	//document service
	//setting up NN
	modelhandler := nn_model_handler.NewHttpModelHandler(log, config.Model.Route)
	model := nn_adapter.NewDetectionModel(modelhandler)

	reportCreator := report_creator.NewPDFReportCreator(config.ReportCreatorPath)
	reportCreatorService := service.NewDocumentService(log, model, annotTypeRepo, reportCreator)

	documentStorage := storage.NewDocumentRepositoryAdapter(config.DocumentPath, config.DocumentExt)

	reportStorage := storage.NewDocumentRepositoryAdapter(config.ReportPath, config.ReportExt)

	documentRepo := repo_adapter.NewDocumentRepositoryAdapter(db)
	documentService := service.NewDocumentService(log, documentRepo, documentStorage, reportStorage, reportCreatorService)

	//userService 0_0
	userService := service.NewUserService(log, userRepo)

	//handlers
	userHandler := user_handler.NewDocumentHandler(log, userService)
	documentHandler := document_handler.NewDocumentHandler(log, documentService)
	annotHandler := annot_handler.NewAnnotHandler(log, annotService)
	annotTypeHandler := annot_type_handler.NewAnnotTypehandler(log, annotTypeService)

	authHandler := auth_handler.NewAuthHandler(log, authService)
	//auth service
	router := chi.NewRouter()
	//router.Use(middleware.Logger)

	authMiddleware := auth_middleware.NewJwtAuthMiddleware(log, service.SECRET, tokenHandler)
	accesMiddleware := access_middleware.NewAccessMiddleware(log, userService)

	router.Group(func(r chi.Router) { // group for which auth middleware is required
		r.Use(authMiddleware.MiddlewareFunc)

		// Document
		r.Route("/document", func(r chi.Router) {
			r.Post("/report", documentHandler.CreateReport())
			r.Get("/getDocument", documentHandler.GetDocumentByID())
			r.Get("/getReport", documentHandler.GetReportByID())
			r.Get("/getDocumentsMeta", documentHandler.GetDocumentsMetaData())
		})

		// AnnotType
		r.Route("/annotType", func(r chi.Router) {
			r.Use(accesMiddleware.ControllersAndHigherMiddleware) // apply the desired middleware here

			adminOnlyAnnotTypes := r.Group(nil)
			adminOnlyAnnotTypes.Use(accesMiddleware.AdminOnlyMiddleware)

			r.Post("/add", annotTypeHandler.AddAnnotType())
			r.Get("/get", annotTypeHandler.GetAnnotType())

			r.Get("/creatorID", annotTypeHandler.GetAnnotTypesByCreatorID())

			r.Get("/gets", annotTypeHandler.GetAnnotTypesByIDs())

			adminOnlyAnnotTypes.Delete("/delete", annotTypeHandler.DeleteAnnotType())
			r.Get("/getsAll", annotTypeHandler.GetAllAnnotTypes())

		})
		//Annot
		r.Route("/annot", func(r chi.Router) {
			r.Use(accesMiddleware.ControllersAndHigherMiddleware)
			//adminOnlyAnnots := r.Group(nil)
			//adminOnlyAnnots.Use(accesMiddleware.AdminOnlyMiddleware)

			r.Post("/add", annotHandler.AddAnnot())
			r.Get("/get", annotHandler.GetAnnot())
			r.Get("/creatorID", annotHandler.GetAnnotsByUserID())

			r.Delete("/delete", annotHandler.DeleteAnnot())
			r.Get("/getsAll", annotHandler.GetAllAnnots())
		})
		//user
		r.Route("/user", func(r chi.Router) {
			r.Use(accesMiddleware.AdminOnlyMiddleware)
			r.Post("/role", userHandler.ChangeUserPerms())
			r.Get("/getUsers", userHandler.GetAllUsers())
		})

	})

	//auth, no middleware is required
	router.Post("/user/SignUp", authHandler.SignUp())
	router.Post("/user/SignIn", authHandler.SignIn())

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	srv := &http.Server{
		Addr:         config.Addr,
		Handler:      router,
		ReadTimeout:  config.ReadTimeout,
		WriteTimeout: config.WriteTimeout,
		IdleTimeout:  config.IdleTimeout,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil {
			fmt.Println("error with server")
		}
	}()

	<-done
}
