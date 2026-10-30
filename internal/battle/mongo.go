package battle

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// MongoStore persists battles in a MongoDB collection.
type MongoStore struct {
	client *mongo.Client
	coll   *mongo.Collection
}

// OpenMongo connects and prepares the battles collection.
func OpenMongo(ctx context.Context, uri, db string) (*MongoStore, error) {
	if uri == "" {
		return nil, errors.New("battle: empty mongo uri")
	}
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("battle mongo connect: %w", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("battle mongo ping: %w", err)
	}
	coll := client.Database(db).Collection("battles")
	if _, err := coll.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "world", Value: 1}, {Key: "season", Value: 1}, {Key: "createdAt", Value: -1}},
			Options: options.Index().SetName("world_season_created"),
		},
		{
			Keys:    bson.D{{Key: "attacker", Value: 1}, {Key: "season", Value: 1}, {Key: "createdAt", Value: -1}},
			Options: options.Index().SetName("attacker_season_created"),
		},
		{
			Keys:    bson.D{{Key: "defender", Value: 1}, {Key: "season", Value: 1}, {Key: "createdAt", Value: -1}},
			Options: options.Index().SetName("defender_season_created"),
		},
	}); err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("battle mongo index: %w", err)
	}
	_, _ = coll.Indexes().DropOne(ctx, "attacker_created")
	_, _ = coll.Indexes().DropOne(ctx, "defender_created")
	return &MongoStore{client: client, coll: coll}, nil
}

// Close releases the client.
func (s *MongoStore) Close(ctx context.Context) error { return s.client.Disconnect(ctx) }

// Create inserts a battle.
func (s *MongoStore) Create(ctx context.Context, b *Battle) error {
	if _, err := s.coll.InsertOne(ctx, b); err != nil {
		return fmt.Errorf("battle insert: %w", err)
	}
	return nil
}

// Get loads one battle by id.
func (s *MongoStore) Get(ctx context.Context, id string) (*Battle, error) {
	var b Battle
	if err := s.coll.FindOne(ctx, bson.M{"_id": id}).Decode(&b); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &b, nil
}

// ListByPlayer returns a player's battles, newest first, with a compound
// (createdAt, id) cursor.
func (s *MongoStore) ListByPlayer(ctx context.Context, playerID uint64, season uint32, before int64, beforeID string, limit int64) ([]Battle, error) {
	if limit <= 0 {
		limit = 20
	}
	and := []bson.M{{"$or": []bson.M{{"attacker": playerID}, {"defender": playerID}}}}
	if season != 0 {
		and = append(and, bson.M{"season": season})
	}
	if before > 0 {
		cond := bson.M{"createdAt": bson.M{"$lt": before}}
		if beforeID != "" {
			cond = bson.M{"$or": []bson.M{
				{"createdAt": bson.M{"$lt": before}},
				{"createdAt": before, "_id": bson.M{"$lt": beforeID}},
			}}
		}
		and = append(and, cond)
	}
	cur, err := s.coll.Find(ctx, bson.M{"$and": and}, options.Find().
		SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}).
		SetLimit(limit))
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	var out []Battle
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}
