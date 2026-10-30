package rank

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// PowerRow is one player's row in the player_power projection.
type PowerRow struct {
	PID     uint64 `bson:"pid"`
	Power   int64  `bson:"power"`
	Tie     int64  `bson:"tie"`
	Version uint64 `bson:"version"`
}

// Projection loads the rows used to rebuild a world's leaderboard.
type Projection interface {
	Load(ctx context.Context, worldID, season uint32) ([]PowerRow, error)
}

// RebuildFrom loads the projection and replaces the board.
func (w *WorldRankActor) RebuildFrom(ctx context.Context, p Projection, worldID, season uint32) (int, error) {
	rows, err := p.Load(ctx, worldID, season)
	if err != nil {
		return 0, err
	}
	entries := make([]Entry, 0, len(rows))
	versions := make(map[uint64]uint64, len(rows))
	for _, r := range rows {
		entries = append(entries, Entry{PID: r.PID, Power: r.Power, Tie: r.Tie})
		versions[r.PID] = r.Version
	}
	w.Rebuild(entries, versions)
	return len(rows), nil
}

// MongoProjection reads player_power from MongoDB.
type MongoProjection struct {
	coll *mongo.Collection
}

// NewMongoProjection builds a projection over db.player_power.
func NewMongoProjection(client *mongo.Client, db string) *MongoProjection {
	return &MongoProjection{coll: client.Database(db).Collection("player_power")}
}

// Load implements Projection. player_power is world-scoped; season is accepted
// for future season-scoped projections.
func (m *MongoProjection) Load(ctx context.Context, worldID, season uint32) ([]PowerRow, error) {
	_ = season
	cur, err := m.coll.Find(ctx, bson.M{"world": worldID})
	if err != nil {
		return nil, fmt.Errorf("player_power find: %w", err)
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []PowerRow
	if err := cur.All(ctx, &rows); err != nil {
		return nil, fmt.Errorf("player_power decode: %w", err)
	}
	return rows, nil
}
