package filtrx

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/smartystreets/goconvey/convey"
)

func TestDelete(t *testing.T) {
	Convey("Given a filtered delete", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(`DELETE FROM "users" WHERE "status" = $1`).
			WithArgs("banned").
			WillReturnResult(sqlmock.NewResult(0, 3))

		q := From("users").Where(struct {
			Status Text `col:"status"`
		}{Status: Text{Eq: Some("banned")}})

		Convey("When deleted", func() {
			n, err := q.Delete(context.Background(), db)
			Convey("Then it runs the DELETE and returns rows affected", func() {
				So(err, ShouldBeNil)
				So(n, ShouldEqual, 3)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})

	Convey("Given a delete with no filter", t, func() {
		db, _ := newMock(t)
		defer func() { _ = db.Close() }()

		q := From("users")
		Convey("When deleted without Unfiltered", func() {
			_, err := q.Delete(context.Background(), db)
			Convey("Then it refuses, as a compile error", func() {
				So(err, ShouldNotBeNil)
				So(errors.Is(err, ErrCompile), ShouldBeTrue)
			})
		})
	})

	Convey("Given an explicit whole-table delete", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(`DELETE FROM "sessions"`).
			WillReturnResult(sqlmock.NewResult(0, 10))

		q := From("sessions").Unfiltered()
		Convey("When deleted", func() {
			n, err := q.Delete(context.Background(), db)
			Convey("Then Unfiltered authorises it", func() {
				So(err, ShouldBeNil)
				So(n, ShouldEqual, 10)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})
}

func TestUpdate(t *testing.T) {
	Convey("Given a filtered update of two columns", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		// Columns sorted: name, status (SET); WHERE follows at $3.
		mock.ExpectExec(`UPDATE "users" SET "name" = $1, "status" = $2 WHERE "id" = $3`).
			WithArgs("bob", "active", 7).
			WillReturnResult(sqlmock.NewResult(0, 1))

		q := From("users").Cond(Eq("id", 7))
		Convey("When updated", func() {
			n, err := q.Update(context.Background(), db, map[string]any{
				"status": "active",
				"name":   "bob",
			})
			Convey("Then SET is deterministic and WHERE placeholders follow it", func() {
				So(err, ShouldBeNil)
				So(n, ShouldEqual, 1)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})

	Convey("Given an update with an empty set", t, func() {
		db, _ := newMock(t)
		defer func() { _ = db.Close() }()

		_, err := From("users").Cond(Eq("id", 1)).Update(context.Background(), db, nil)
		Convey("When updated", func() {
			Convey("Then it errors for the empty set", func() {
				So(err, ShouldNotBeNil)
				So(errors.Is(err, ErrCompile), ShouldBeTrue)
			})
		})
	})

	Convey("Given an update with no filter", t, func() {
		db, _ := newMock(t)
		defer func() { _ = db.Close() }()

		_, err := From("users").Update(context.Background(), db, map[string]any{"x": 1})
		Convey("When updated without Unfiltered", func() {
			Convey("Then it refuses", func() {
				So(err, ShouldNotBeNil)
				So(errors.Is(err, ErrCompile), ShouldBeTrue)
			})
		})
	})
}

func TestMutationRejectsJoinedSource(t *testing.T) {
	Convey("Given a query whose source is a joined filter", t, func() {
		db, _ := newMock(t)
		defer func() { _ = db.Close() }()

		q := For(userWithOrders{Status: Text{Eq: Some("active")}})

		Convey("When deleting", func() {
			_, derr := q.Delete(context.Background(), db)
			Convey("Then it refuses an aliased/joined source", func() {
				So(derr, ShouldNotBeNil)
				So(errors.Is(derr, ErrCompile), ShouldBeTrue)
			})
		})

		Convey("When updating", func() {
			_, uerr := For(userWithOrders{Status: Text{Eq: Some("active")}}).
				Update(context.Background(), db, map[string]any{"x": 1})
			Convey("Then it also refuses", func() {
				So(uerr, ShouldNotBeNil)
				So(errors.Is(uerr, ErrCompile), ShouldBeTrue)
			})
		})
	})
}
