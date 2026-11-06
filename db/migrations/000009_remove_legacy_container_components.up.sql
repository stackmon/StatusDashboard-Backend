-- The legacy "Cloud Container Service" rows predate the component attribute
-- contract and duplicate Cloud Container Engine. Only remove rows that have no
-- attributes and are not referenced by an incident, so a real service or a
-- referenced component is never dropped.
DELETE FROM component c
WHERE c.name = 'Cloud Container Service'
  AND NOT EXISTS (
      SELECT 1
      FROM component_attribute a
      WHERE a.component_id = c.id
  )
  AND NOT EXISTS (
      SELECT 1
      FROM incident_component_relation r
      WHERE r.component_id = c.id
  );
