package trade

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// TestMongoReplicaSetTransaction requires a single-node replica set and
// MONGO_REPLSET_URL, e.g. mongodb://127.0.0.1:27018/?replicaSet=rs0.
// MONGO_REPLSET_ADMIN_URL is used to initiate the set when needed.
func TestMongoReplicaSetTransaction(t *testing.T) {
	uri := os.Getenv("MONGO_REPLSET_URL")
	if uri == "" {
		t.Skip("MONGO_REPLSET_URL not set")
	}
	adminURI := os.Getenv("MONGO_REPLSET_ADMIN_URL")
	if adminURI == "" {
		adminURI = "mongodb://127.0.0.1:27018/?directConnection=true"
	}
	host := os.Getenv("MONGO_REPLSET_HOST")
	if host == "" {
		host = "127.0.0.1:27018"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	admin, err := mongo.Connect(ctx, options.Client().ApplyURI(adminURI))
	if err != nil {
		t.Fatal(err)
	}
	err = admin.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}}).Err()
	if err != nil {
		cfg := bson.D{
			{Key: "_id", Value: "rs0"},
			{Key: "members", Value: bson.A{bson.D{
				{Key: "_id", Value: 0},
				{Key: "host", Value: host},
			}}},
		}
		if err := admin.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetInitiate", Value: cfg}}).Err(); err != nil {
			t.Fatalf("replSetInitiate: %v", err)
		}
	}
	_ = admin.Disconnect(ctx)

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	for i := 0; i < 60; i++ {
		var hello bson.M
		if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err == nil {
			if primary, _ := hello["isWritablePrimary"].(bool); primary {
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	db := fmt.Sprintf("hapeserver_rs_%d", time.Now().UnixNano())
	coll := client.Database(db).Collection("tx")
	sess, err := client.StartSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.EndSession(context.Background())
	_, err = sess.WithTransaction(ctx, func(sc mongo.SessionContext) (any, error) {
		if _, err := coll.InsertOne(sc, bson.M{"_id": "a", "v": 1}); err != nil {
			return nil, err
		}
		if _, err := coll.InsertOne(sc, bson.M{"_id": "b", "v": 2}); err != nil {
			return nil, err
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("transaction: %v", err)
	}
	n, err := coll.CountDocuments(ctx, bson.M{})
	if err != nil || n != 2 {
		t.Fatalf("count=%d err=%v", n, err)
	}
	_ = coll.Drop(context.Background())
}
