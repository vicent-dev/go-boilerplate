# Go-boilerplate

Basic boilerplate for go projects using gorilla mux and gorm.

## Project structure 

### cmd/

Entry points of application. Prepared for gorilla mux http server.

### app/

Folder to place main struct of the project. The struct `server` has all the required resources that needs to be shared to can run the application. It contains database access, router and general configurations, so it's our IO / Infrastructure layer of the application. 

### pkg/

Also known as `internal` in so many FOSS projects it may contain our domain/application logic in most of the cases. Inside pkg you expect to see the folders of our bounded contexts and inside it, the different files of the package. This one could be a good example:

```bash
pkg/
    user/
        user.go         // models and domain interfaces related to user bounded context
        gorm.go         // gorm repository implementation
        redis.go        // redis repository implementation
        services.go     // application use cases and application DTOs (forms mappings). In case the services file became too big or too complex you should to consider to split the file in smaller ones.
```

### static/

Static files of the application like `yaml` and `html` files. It uses `embed` package with the final purpouse of having only a stand-alone single binary file ready to production.

## Install

```bash
git clone git@github.com:vicent-dev/go-boilerplate.git &&
cd go-boilerplate &&
bash install.sh
```

## Run

```bash
λ go-boilerplate
Insert project name: test_project
```
