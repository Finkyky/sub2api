package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// Use real constructors for existing non-nil cleanup dependencies. Reflection
// lets the RED suite assert an absent dependency without a compiler failure.
func pluginCleanupFixture(t *testing.T, instances *service.PluginInstanceService, manager *service.PluginManager, extras ...any) func() {
	t.Helper()
	cfg := &config.Config{}
	oauth := service.NewOAuthService(nil, nil)
	openai := service.NewOpenAIOAuthService(nil, nil)
	gemini := service.NewGeminiOAuthService(nil, nil, nil, nil, cfg)
	antigravity := service.NewAntigravityOAuthService(nil)
	dependencies := []any{
		service.NewTokenRefreshService(nil, oauth, openai, gemini, antigravity, nil, nil, cfg, nil),
		service.NewAccountExpiryService(nil, time.Second),
		service.NewOpenAICodexVersionSyncService(nil, nil, nil, time.Second),
		service.NewProxyExpiryService(nil, time.Second),
		service.NewSubscriptionExpiryService(nil, time.Second),
		service.NewPricingService(cfg, nil), service.NewEmailQueueService(nil, 1),
		service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil),
		oauth, openai, gemini, antigravity, instances, manager,
	}
	dependencies = append(dependencies, extras...)
	values := map[reflect.Type]reflect.Value{}
	for _, dependency := range dependencies {
		values[reflect.TypeOf(dependency)] = reflect.ValueOf(dependency)
	}
	provider := reflect.ValueOf(provideCleanup)
	args := make([]reflect.Value, provider.Type().NumIn())
	for i := range args {
		typ := provider.Type().In(i)
		args[i] = reflect.Zero(typ)
		if value, ok := values[typ]; ok {
			args[i] = value
		}
	}
	return provider.Call(args)[0].Interface().(func())
}
func pluginCleanupFunction(t *testing.T, file, name string) *ast.FuncDecl {
	t.Helper()
	source, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	require.NoError(t, err)
	for _, decl := range source.Decls {
		if f, ok := decl.(*ast.FuncDecl); ok && f.Name.Name == name {
			return f
		}
	}
	t.Fatalf("missing function %s in %s", name, file)
	return nil
}
func pluginCleanupHasDependency() bool {
	typ := reflect.TypeOf(provideCleanup)
	want := reflect.TypeOf((*service.PluginInstanceService)(nil))
	for i := 0; i < typ.NumIn(); i++ {
		if typ.In(i) == want {
			return true
		}
	}
	return false
}
func TestPluginCleanupDependencyContract(t *testing.T) {
	require.True(t, pluginCleanupHasDependency(), "production cleanup must depend on PluginInstanceService")
}
func TestPluginCleanupClosesInstanceService(t *testing.T) {
	instances := service.NewPluginInstanceService(nil, nil, nil, time.Second)
	defer instances.Close()
	require.NoError(t, instances.StartReconciler(context.Background(), time.Millisecond, time.Hour))
	cleanup := pluginCleanupFixture(t, instances, nil)
	app := &Application{Cleanup: cleanup}
	app.Cleanup()
	require.ErrorIs(t, instances.Reconcile(context.Background()), service.ErrGeneralClosed, "application cleanup must close the actual instance service")
	require.ErrorIs(t, instances.StartReconciler(context.Background(), time.Millisecond, time.Second), service.ErrGeneralClosed)
}
func TestPluginCleanupSourceOnceContract(t *testing.T) {
	for _, file := range []string{"wire.go", "wire_gen.go"} {
		t.Run(file, func(t *testing.T) {
			function := pluginCleanupFunction(t, file, "provideCleanup")
			hasOnce, hasDo := false, false
			ast.Inspect(function.Body, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if id, ok := selector.X.(*ast.Ident); ok {
					if id.Name == "sync" && selector.Sel.Name == "Once" {
						hasOnce = true
					}
					if selector.Sel.Name == "Do" {
						hasDo = true
					}
				}
				return true
			})
			require.True(t, hasOnce && hasDo, "whole cleanup needs a sync.Once guard, not only one service")
		})
	}
}
func TestPluginCleanupWireGraphUsesSharedInstance(t *testing.T) {
	initialize := pluginCleanupFunction(t, "wire_gen.go", "initializeApplication")
	produced, handlerValue, cleanupValue := "", "", ""
	providerCount := 0
	ast.Inspect(initialize.Body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if ok && len(assignment.Rhs) == 1 {
			call, ok := assignment.Rhs[0].(*ast.CallExpr)
			if ok {
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if ok && sel.Sel.Name == "ProvidePluginInstanceService" {
					providerCount++
					produced = assignment.Lhs[0].(*ast.Ident).Name
				}
			}
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "NewPluginInstanceHandler" && len(call.Args) > 0 {
			handlerValue = call.Args[0].(*ast.Ident).Name
		}
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "provideCleanup" {
			for _, arg := range call.Args {
				if id, ok := arg.(*ast.Ident); ok && id.Name == produced {
					cleanupValue = id.Name
				}
			}
		}
		return true
	})
	require.Equal(t, 1, providerCount)
	require.NotEmpty(t, produced)
	require.Equal(t, produced, handlerValue)
	require.Equal(t, produced, cleanupValue, "handler and cleanup must receive the same Wire-produced instance")
}

type pluginCleanupLog struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (w *pluginCleanupLog) Write(value []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.Write(value)
}
func (w *pluginCleanupLog) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}
func pluginCleanupCapture(t *testing.T) *pluginCleanupLog {
	t.Helper()
	capture := &pluginCleanupLog{}
	previous := log.Writer()
	log.SetOutput(capture)
	t.Cleanup(func() { log.SetOutput(previous) })
	return capture
}
func TestPluginCleanupConcurrentAndRepeatedCallExactlyOnce(t *testing.T) {
	capture := pluginCleanupCapture(t)
	instances := service.NewPluginInstanceService(nil, nil, nil, time.Second)
	defer instances.Close()
	cleanup := pluginCleanupFixture(t, instances, nil)
	var workers sync.WaitGroup
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() { defer workers.Done(); cleanup() }()
	}
	workers.Wait()
	cleanup()
	cleanup()
	require.ErrorIs(t, instances.Reconcile(context.Background()), service.ErrGeneralClosed)
	text := capture.String()
	for _, step := range []string{"PluginManager", "Redis", "Ent", "All cleanup steps completed"} {
		require.Equal(t, 1, strings.Count(text, "[Cleanup] "+step), step)
	}
}
func TestPluginCleanupPluginOrderingAndInfrastructureLast(t *testing.T) {
	for _, file := range []string{"wire.go", "wire_gen.go"} {
		t.Run(file, func(t *testing.T) {
			function := pluginCleanupFunction(t, file, "provideCleanup")
			ordered := false
			afterParallel := false
			ast.Inspect(function.Body, func(node ast.Node) bool {
				literal, ok := node.(*ast.FuncLit)
				if !ok {
					return true
				}
				closePos, stopPos := token.NoPos, token.NoPos
				ast.Inspect(literal.Body, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					id, ok := sel.X.(*ast.Ident)
					if !ok {
						return true
					}
					if id.Name == "pluginInstances" && sel.Sel.Name == "Close" {
						closePos = call.Pos()
					}
					if id.Name == "pluginManager" && sel.Sel.Name == "Stop" {
						stopPos = call.Pos()
					}
					return true
				})
				if closePos != token.NoPos && stopPos > closePos {
					ordered = true
				}
				return true
			})
			parallelPos, sequentialPos := token.NoPos, token.NoPos
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if ok {
					id, ok := call.Fun.(*ast.Ident)
					if ok {
						if id.Name == "runParallel" {
							parallelPos = call.Pos()
						}
						if id.Name == "runSequential" {
							sequentialPos = call.Pos()
						}
					}
				}
				return true
			})
			afterParallel = parallelPos != token.NoPos && sequentialPos > parallelPos
			require.True(t, ordered, "instances Close must precede manager Stop in the same plugin branch")
			require.True(t, afterParallel, "infrastructure must remain after all parallel application services")
		})
	}
}

type pluginCleanupBlockingRepo struct {
	service.PluginInstanceRepository
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *pluginCleanupBlockingRepo) List(context.Context, int64) ([]service.PluginInstance, error) {
	r.once.Do(func() { close(r.entered) })
	<-r.release
	return nil, nil
}
func TestPluginCleanupConcurrentCallerWaitsAndInfrastructureStaysOpen(t *testing.T) {
	capture := pluginCleanupCapture(t)
	repo := &pluginCleanupBlockingRepo{entered: make(chan struct{}), release: make(chan struct{})}
	instances := service.NewPluginInstanceService(repo, nil, nil, time.Second)
	operation := make(chan error, 1)
	go func() { _, err := instances.List(context.Background(), 0); operation <- err }()
	<-repo.entered
	cleanup := pluginCleanupFixture(t, instances, nil)
	first, second := make(chan struct{}), make(chan struct{})
	go func() { cleanup(); close(first) }()
	require.Eventually(t, func() bool {
		return errors.Is(instances.StartReconciler(context.Background(), time.Millisecond, time.Second), service.ErrGeneralClosed)
	}, time.Second, time.Millisecond)
	go func() { cleanup(); close(second) }()
	defer func() { close(repo.release); <-operation; <-first; <-second; instances.Close() }()
	time.Sleep(20 * time.Millisecond)
	for _, done := range []chan struct{}{first, second} {
		select {
		case <-done:
			t.Fatal("cleanup returned before the owner finished closing")
		default:
		}
	}
	require.NotContains(t, capture.String(), "[Cleanup] Redis")
	require.NotContains(t, capture.String(), "[Cleanup] PluginManager")
}

type pluginCleanupManagerRepo struct {
	service.PluginRepository
	instances *service.PluginInstanceService
	entered   chan struct{}
	observed  chan error
	once      sync.Once
}

func (r *pluginCleanupManagerRepo) List(ctx context.Context) ([]*service.PluginInstallation, error) {
	r.once.Do(func() { close(r.entered) })
	<-ctx.Done()
	r.observed <- r.instances.Reconcile(context.Background())
	return nil, ctx.Err()
}
func TestPluginCleanupManagerCancellationFollowsInstanceClose(t *testing.T) {
	instances := service.NewPluginInstanceService(nil, nil, nil, time.Second)
	defer instances.Close()
	repo := &pluginCleanupManagerRepo{instances: instances, entered: make(chan struct{}), observed: make(chan error, 4)}
	cfg := &config.Config{}
	cfg.Plugins.DataDir = t.TempDir()
	manager := service.NewPluginManager(repo, nil, cfg, service.PluginHostInfo{}, nil)
	started := make(chan error, 1)
	go func() { started <- manager.Start(context.Background()) }()
	<-repo.entered
	cleanup := pluginCleanupFixture(t, instances, manager)
	cleanup()
	require.NoError(t, <-started)
	require.ErrorIs(t, <-repo.observed, service.ErrGeneralClosed, "the manager must not cancel before instances close")
	cleanup()
}
func TestPluginCleanupInfrastructureClosesOnceAndAfterPluginServices(t *testing.T) {
	capture := pluginCleanupCapture(t)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.ExpectClose()
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	redisClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	instances := service.NewPluginInstanceService(nil, nil, nil, time.Second)
	defer instances.Close()
	cleanup := pluginCleanupFixture(t, instances, nil, client, redisClient)
	cleanup()
	cleanup()
	require.NoError(t, mock.ExpectationsWereMet())
	require.ErrorContains(t, redisClient.Ping(context.Background()).Err(), "closed")
	require.ErrorIs(t, instances.Reconcile(context.Background()), service.ErrGeneralClosed)
	text := capture.String()
	pluginAt := strings.Index(text, "[Cleanup] PluginManager succeeded")
	redisAt := strings.Index(text, "[Cleanup] Redis succeeded")
	entAt := strings.Index(text, "[Cleanup] Ent succeeded")
	require.True(t, pluginAt >= 0 && redisAt > pluginAt && entAt > redisAt)
}
func TestPluginCleanupInfrastructureFailureDoesNotRepeatShutdown(t *testing.T) {
	capture := pluginCleanupCapture(t)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.ExpectClose().WillReturnError(errors.New("fixture database close failure"))
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	cleanup := pluginCleanupFixture(t, nil, nil, client)
	cleanup()
	cleanup()
	require.NoError(t, mock.ExpectationsWereMet())
	require.Contains(t, capture.String(), "[Cleanup] Ent failed: fixture database close failure")
	require.Equal(t, 1, strings.Count(capture.String(), "[Cleanup] All cleanup steps completed"))
}
func TestPluginCleanupRedisFailureStillClosesEntOnce(t *testing.T) {
	capture := pluginCleanupCapture(t)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.ExpectClose()
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	redisClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	require.NoError(t, redisClient.Close()) // A second Close returns redis.ErrClosed.
	cleanup := pluginCleanupFixture(t, nil, nil, client, redisClient)
	cleanup()
	cleanup()
	require.NoError(t, mock.ExpectationsWereMet(), "Ent must close even after Redis fails")
	text := capture.String()
	redisAt := strings.Index(text, "[Cleanup] Redis failed: "+redis.ErrClosed.Error())
	entAt := strings.Index(text, "[Cleanup] Ent succeeded")
	require.True(t, redisAt >= 0 && entAt > redisAt, "Redis failure must not stop infrastructure cleanup")
	require.Equal(t, 1, strings.Count(text, "[Cleanup] Redis failed:"))
	require.Equal(t, 1, strings.Count(text, "[Cleanup] Ent succeeded"))
	require.Equal(t, 1, strings.Count(text, "[Cleanup] All cleanup steps completed"))
}
func TestPluginCleanupNilPluginDependencies(t *testing.T) {
	for _, useInstances := range []bool{false, true} {
		for _, useManager := range []bool{false, true} {
			t.Run(fmt.Sprint(useInstances, "_", useManager), func(t *testing.T) {
				var instances *service.PluginInstanceService
				var manager *service.PluginManager
				if useInstances {
					instances = service.NewPluginInstanceService(nil, nil, nil, time.Second)
					defer instances.Close()
				}
				if useManager {
					manager = service.NewPluginManager(nil, nil, &config.Config{}, service.PluginHostInfo{}, nil)
				}
				cleanup := pluginCleanupFixture(t, instances, manager)
				require.NotPanics(t, func() { cleanup(); cleanup() })
			})
		}
	}
}
