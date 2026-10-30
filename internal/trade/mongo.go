package trade

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ErrConflict means the document version/state moved under us.
var ErrConflict = errors.New("trade conflict")

// ErrNotFound means the trade document does not exist.
var ErrNotFound = errors.New("trade not found")

// Repo persists trades in MongoDB. The document's version is the CAS guard.
type Repo struct {
	client *mongo.Client
	coll   *mongo.Collection
}

// OpenMongo connects and prepares the trades collection.
func OpenMongo(ctx context.Context, uri, db string) (*Repo, error) {
	if uri == "" {
		return nil, errors.New("trade: empty mongo uri")
	}
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("mongo ping: %w", err)
	}
	r := &Repo{client: client, coll: client.Database(db).Collection("trades")}
	if _, err := r.coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "state", Value: 1}},
	}); err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("mongo index: %w", err)
	}
	return r, nil
}

// Close releases the client.
func (r *Repo) Close(ctx context.Context) error { return r.client.Disconnect(ctx) }

// Create inserts a new trade document.
func (r *Repo) Create(ctx context.Context, t *Trade) error {
	if _, err := r.coll.InsertOne(ctx, t); err != nil {
		return fmt.Errorf("trade insert: %w", err)
	}
	return nil
}

// Get loads one trade by id.
func (r *Repo) Get(ctx context.Context, id string) (*Trade, error) {
	var t Trade
	if err := r.coll.FindOne(ctx, bson.M{"_id": id}).Decode(&t); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

// Transition performs a compare-and-swap on (state, version).
func (r *Repo) Transition(ctx context.Context, id string, from, to State, version uint64) error {
	if version == 0 {
		return errors.New("trade: version must be >= 1")
	}
	res, err := r.coll.UpdateOne(ctx,
		bson.M{"_id": id, "state": from, "version": version - 1},
		bson.M{"$set": bson.M{"state": to, "version": version}},
	)
	if err != nil {
		return fmt.Errorf("trade transition: %w", err)
	}
	if res.MatchedCount == 0 {
		return ErrConflict
	}
	return nil
}

// ListPending returns trades that still need settlement or refund.
func (r *Repo) ListPending(ctx context.Context, limit int64) ([]Trade, error) {
	if limit <= 0 {
		limit = 100
	}
	cur, err := r.coll.Find(ctx,
		bson.M{"state": bson.M{"$in": []State{ALocked, BothLocked, Settling, Refunding}}},
		options.Find().SetLimit(limit),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	var out []Trade
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}
