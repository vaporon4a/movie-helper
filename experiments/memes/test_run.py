import unittest
import run
class PilotContract(unittest.TestCase):
 def test_review_keys_and_index_contract(self):
  good={'index':0,'text':'','evidence':'','reviews':{'0':{'reason':'accepted','detail':'Поворот шутки'},'1':{'reason':'not_meme','detail':'Нет шутки'}}}
  run.validate(good,2)
  for bad in [{**good,'text':'invented'},{**good,'index':1},{**good,'index':-1},{**good,'reviews':{'0':good['reviews']['0']}}]:
   with self.assertRaises(ValueError):run.validate(bad,2)
 def test_no_candidate_is_valid(self):
  run.validate({'index':-1,'text':'','evidence':'','reviews':{'0':{'reason':'low_humor','detail':'Слабая шутка'}}},1)
 def test_schema_is_closed_per_index(self):
  schema=run.schema(2)
  self.assertFalse(schema['additionalProperties'])
  self.assertEqual(schema['properties']['reviews']['required'],['0','1'])
if __name__=='__main__':unittest.main()
